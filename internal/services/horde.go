package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
		horde.WithTimeout(cfg.GetHordeTimeout()),
	)

	return &HordeService{
		client: client,
		cfg:    cfg,
		logger: logger,
	}
}

// JobURL returns the Horde dashboard URL of a job, as shown to Swarm users
func (s *HordeService) JobURL(jobID string) string {
	base := s.cfg.Horde.PublicURL
	if base == "" {
		base = s.cfg.Horde.Host
	}
	return fmt.Sprintf("%s/job/%s", strings.TrimRight(base, "/"), jobID)
}

// NewCreateJobRequest builds the Horde job creation request for a shelved change
func NewCreateJobRequest(cfg *config.Config, change string) horde.CreateJobRequest {
	change = strings.TrimSpace(change)
	req := horde.CreateJobRequest{
		TemplateId:        cfg.Horde.TemplateId,
		StreamId:          cfg.Horde.StreamId,
		Name:              fmt.Sprintf("Swarm preflight CL %s", change),
		PreflightCommitId: change,
		AutoSubmit:        false,
	}
	// The obsolete integer field is only sent when the change is numeric
	if n, err := strconv.Atoi(change); err == nil && n > 0 {
		req.PreflightChange = n
	}
	return req
}

// CreateJob creates a new job in the Horde system
func (s *HordeService) CreateJob(ctx context.Context, change string) (string, error) {
	s.logger.Debug().Msgf("Preparing job creation request for change: %s", change)

	req := NewCreateJobRequest(s.cfg, change)
	s.logger.Debug().Msgf("Job creation request payload: %+v", req)

	jobID, err := withRetry(ctx, s, func() (string, error) {
		return s.client.CreateJob(ctx, req)
	})
	if err != nil {
		return "", fmt.Errorf("creating horde job: %w", err)
	}

	s.logger.Debug().
		Str("jobID", jobID).
		Str("change", change).
		Msg("created horde job")

	return jobID, nil
}

// GetJobStatus retrieves the current status of a job, with a human readable
// reason when the job failed
func (s *HordeService) GetJobStatus(ctx context.Context, jobID string) (models.JobStatus, string, error) {
	s.logger.Debug().Str("job_id", jobID).Msg("Starting GetJobStatus for job.")

	resp, err := withRetry(ctx, s, func() (horde.GetJobResponse, error) {
		s.logger.Debug().Str("job_id", jobID).Msg("Sending request to Horde to get job status.")
		return s.client.GetJobStatus(ctx, jobID)
	})
	if err != nil {
		return models.StatusUnknown, "", fmt.Errorf("getting horde job status: %w", err)
	}

	s.logger.Debug().
		Interface("response", resp).
		Msg("Detailed job response from Horde")

	status, reason := EvaluateJob(resp)
	s.logger.Debug().
		Str("job_id", jobID).
		Str("state", resp.State).
		Str("mapped_status", string(status)).
		Str("reason", reason).
		Msg("Retrieved and mapped job status from Horde.")

	return status, reason, nil
}

// EvaluateJob maps a Horde job response to the internal job status.
//
// A final verdict is only given once the job state is Complete, because Horde
// may still retry failed steps or replace incomplete batches while the job is
// running. The only early verdict is a job aborted by a user, which is final.
//
// Once Complete, the job failed if a batch has a fatal error (see
// horde.IsFatalBatchError), or a step that was not retried failed or was
// aborted. Steps in batches that are no longer needed are ignored.
// Waiting maps to pending, Running to running; unknown states map to unknown
// (never to failed).
func EvaluateJob(job horde.GetJobResponse) (models.JobStatus, string) {
	if user, ok := job.AbortedBy(); ok {
		reason := "Horde job was cancelled by " + user
		if r := job.CancellationReasonText(); r != "" {
			reason += ": " + r
		}
		return models.StatusFailed, reason
	}

	if job.State != horde.JobStateComplete {
		return mapHordeState(job.State), ""
	}

	if reason, failed := completedJobFailure(job); failed {
		return models.StatusFailed, reason
	}
	return models.StatusCompleted, ""
}

// completedJobFailure returns why a Complete job failed, if it did
func completedJobFailure(job horde.GetJobResponse) (string, bool) {
	for _, batch := range job.Batches {
		if horde.IsFatalBatchError(batch.Error) {
			return "Horde batch failed with error " + batch.Error, true
		}
	}
	for _, batch := range job.Batches {
		if batch.Error == horde.BatchErrorNoLongerNeeded {
			continue
		}
		for _, step := range batch.Steps {
			if step.WasRetried() {
				continue
			}
			if user, ok := step.AbortedBy(); ok {
				return fmt.Sprintf("Horde step %s was cancelled by %s", stepLabel(step), user), true
			}
			if step.Outcome == horde.OutcomeFailure {
				return fmt.Sprintf("Horde step %s failed", stepLabel(step)), true
			}
		}
	}
	return "", false
}

func stepLabel(step horde.Step) string {
	if step.Name != "" {
		return "'" + step.Name + "'"
	}
	return step.Id
}

// withRetry runs op up to Retry.MaxAttempts times with exponential backoff.
// Errors that cannot succeed on retry (e.g. HTTP 4xx) are returned immediately.
func withRetry[T any](ctx context.Context, s *HordeService, op func() (T, error)) (T, error) {
	var zero T
	var lastErr error

	attempts := s.cfg.Retry.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}

		r, err := op()
		if err == nil {
			return r, nil
		}
		lastErr = err

		if !horde.IsRetryable(err) {
			return zero, err
		}

		if attempt < attempts-1 {
			delay := getBackoffDelay(attempt, s.cfg)
			s.logger.Debug().
				Err(err).
				Int("attempt", attempt+1).
				Dur("delay", delay).
				Msg("retrying operation")

			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return zero, fmt.Errorf("max retries exceeded: %w", lastErr)
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
	case horde.JobStateRunning:
		return models.StatusRunning
	case horde.JobStateComplete:
		return models.StatusCompleted
	case horde.JobStateWaiting:
		return models.StatusPending
	default:
		return models.StatusUnknown
	}
}
