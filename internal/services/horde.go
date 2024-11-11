package services

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/horde"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

// HordeService manages interactions with the Horde CI system
type HordeService struct {
	client *horde.Client
	cfg    *config.Config
	logger zerolog.Logger
}

// NewHordeService creates a new instance of HordeService
func NewHordeService(cfg *config.Config, logger zerolog.Logger) *HordeService {
	client := horde.NewClient(
		cfg.Horde.Host,
		cfg.Horde.APIKey,
		logger,
		horde.WithTimeout(time.Duration(cfg.Horde.Timeout)*time.Second),
	)

	return &HordeService{
		client: client,
		cfg:    cfg,
		logger: logger,
	}
}

// CreateJob creates a new job in the Horde system
func (s *HordeService) CreateJob(ctx context.Context, change string) (string, error) {
	s.logger.Debug().Msgf("Preparing job creation request for change: %s", change)

	req := horde.CreateJobRequest{
		TemplateId:      s.cfg.Horde.TemplateId,
		StreamId:        s.cfg.Horde.StreamId,
		Name:            "swarm-preflight",
		PreflightChange: change,
		AutoSubmit:      false,
	}
	s.logger.Debug().Msgf("Job creation request payload: %+v", req)

	jobID, err := s.withRetry(ctx, func() (string, error) {
		return s.client.CreateJob(ctx, req)
	})
	if err != nil {
		return "", fmt.Errorf("creating horde job: %w", err)
	}

	jobIDStr, ok := jobID.(string)
	if !ok {
		return "", fmt.Errorf("expected jobID to be a string")
	}

	s.logger.Info().Msgf("Horde job created with ID: %s for change: %s", jobIDStr, change)
	s.logger.Debug().Msgf("Job ID returned from Horde: %s", jobIDStr)

	s.logger.Debug().
		Str("jobID", jobIDStr).
		Str("change", change).
		Msg("created horde job")

	return jobIDStr, nil
}

// GetJobStatus retrieves the current status of a job
func (s *HordeService) GetJobStatus(ctx context.Context, jobID string) (models.JobStatus, error) {
	s.logger.Debug().Str("job_id", jobID).Msg("Starting GetJobStatus for job.")

	resp, err := s.withRetry(ctx, func() (horde.GetJobResponse, error) {
		s.logger.Debug().Str("job_id", jobID).Msg("Sending request to Horde to get job status.")
		return s.client.GetJobStatus(ctx, jobID)
	})

	if err != nil {
		s.logger.Error().Err(err).
			Str("job_id", jobID).
			Msg("Error retrieving job status from Horde.")
		return models.StatusUnknown, fmt.Errorf("getting horde job status: %w", err)
	}

	respTyped, ok := resp.(horde.GetJobResponse)
	if !ok {
		s.logger.Error().
			Str("job_id", jobID).
			Msg("Failed to assert response as GetJobResponse.")
		return models.StatusUnknown, fmt.Errorf("expected response to be of type horde.GetJobResponse")
	}

	s.logger.Debug().
		Interface("response", respTyped).
		Msg("Detailed job response from Horde")

	// Check for cancellation
	if wasCancelled(respTyped) {
		s.logger.Info().Str("job_id", jobID).Msg("Job was cancelled.")
		return models.StatusFailed, nil
	}

	// Check for errors in batches
	if hasErrors(respTyped) {
		s.logger.Info().Str("job_id", jobID).Msg("Job has errors in batches.")
		return models.StatusFailed, nil
	}

	// Map job state to internal status
	jobStatus := mapHordeState(respTyped.State)
	s.logger.Debug().
		Str("job_id", jobID).
		Str("state", respTyped.State).
		Str("mapped_status", string(jobStatus)).
		Msg("Retrieved and mapped job status from Horde.")

	return jobStatus, nil
}

// wasCancelled checks if the job or any step in its batches was canceled
func wasCancelled(job horde.GetJobResponse) bool {
	if job.AbortedByUserId != nil {
		return true
	}
	for _, batch := range job.Batches {
		for _, step := range batch.Steps {
			if step.AbortedByUserId != nil {
				return true
			}
		}
	}
	return false
}

// hasErrors checks if any batch and steps in the job have errors
func hasErrors(job horde.GetJobResponse) bool {
	for _, batch := range job.Batches {
		if batch.Error != "None" {
			return true
		}
		for _, step := range batch.Steps {
			if step.Outcome == "Failure" {
				return true
			}
		}
	}
	return false
}

// withRetry implements retry logic for operations
func (s *HordeService) withRetry(ctx context.Context, op interface{}) (interface{}, error) {
	var lastErr error
	var result interface{}

	for attempt := 0; attempt < s.cfg.Retry.MaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			switch fn := op.(type) {
			case func() (string, error):
				if r, err := fn(); err == nil {
					return r, nil
				} else {
					lastErr = err
				}
			case func() (horde.GetJobResponse, error):
				if r, err := fn(); err == nil {
					return r, nil
				} else {
					lastErr = err
				}
			default:
				return nil, fmt.Errorf("unsupported operation type")
			}

			if attempt < s.cfg.Retry.MaxAttempts-1 {
				delay := getBackoffDelay(attempt, s.cfg)
				s.logger.Debug().
					Int("attempt", attempt+1).
					Dur("delay", delay).
					Msg("retrying operation")

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
					continue
				}
			}
		}
	}

	if lastErr == nil {
		return nil, fmt.Errorf("max retries exceeded without specific error")
	}
	return result, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// getBackoffDelay calculates the exponential backoff delay
func getBackoffDelay(attempt int, cfg *config.Config) time.Duration {
	delay := time.Duration(cfg.Retry.InitialDelay) * time.Second * (1 << uint(attempt))
	maxDelay := time.Duration(cfg.Retry.MaxDelay) * time.Second
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

// mapHordeState maps Horde's job state to the internal job status
func mapHordeState(state string) models.JobStatus {
	switch state {
	case "Running":
		return models.StatusRunning
	case "Complete":
		return models.StatusCompleted
	case "Waiting":
		return models.StatusPending
	default:
		return models.StatusUnknown
	}
}
