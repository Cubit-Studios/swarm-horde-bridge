package monitor

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/services"
)

type JobMonitor struct {
	config     *config.Config
	logger     zerolog.Logger
	hordeServ  *services.HordeService
	swarmServ  *services.SwarmService
	jobStorage *services.JobStorage
}

func New(cfg *config.Config, logger zerolog.Logger, jobStorage *services.JobStorage) *JobMonitor {
	return &JobMonitor{
		config:     cfg,
		logger:     logger,
		hordeServ:  services.NewHordeService(cfg, logger),
		swarmServ:  services.NewSwarmService(cfg, logger),
		jobStorage: jobStorage,
	}
}

func (m *JobMonitor) Start(ctx context.Context) {
	m.logger.Debug().Msg("JobMonitor starting...")

	ticker := time.NewTicker(time.Duration(m.config.Monitor.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Debug().Msg("JobMonitor stopping due to context cancellation.")
			return
		case <-ticker.C:
			m.logger.Debug().Msg("JobMonitor tick - checking jobs...")
			m.checkJobs(ctx)
		}
	}
}

func (m *JobMonitor) checkJobs(ctx context.Context) {
	m.logger.Debug().Msg("Checking job statuses...")

	jobs := m.jobStorage.List()
	m.logger.Debug().Int("job_count", len(jobs)).Msg("Total jobs in storage")

	for _, job := range jobs {
		m.logger.Debug().Str("job_id", job.HordeJobID).Str("current_status", string(job.Status)).Msg("Checking job status...")

		currentStatus, err := m.hordeServ.GetJobStatus(ctx, job.HordeJobID)
		if err != nil {
			m.logger.Error().Err(err).
				Str("job_id", job.HordeJobID).
				Msg("failed to get job status")
			continue
		}

		// Skip if status hasn't changed
		if currentStatus == job.Status {
			m.logger.Debug().Str("job_id", job.HordeJobID).Msg("No status change detected, skipping update.")
			continue
		}

		// Update job status
		job.Status = currentStatus
		job.UpdatedAt = time.Now()
		m.jobStorage.Store(job.HordeJobID, job)
		m.logger.Info().Str("job_id", job.HordeJobID).Str("new_status", string(currentStatus)).Msg("Job status updated.")

		// Prepare status update for Swarm
		var swarmStatus string
		var message string

		switch currentStatus {
		case models.StatusCompleted:
			swarmStatus = "pass"
			message = "Horde job completed successfully"
			defer m.jobStorage.Delete(job.HordeJobID)
		case models.StatusFailed:
			swarmStatus = "fail"
			message = "Horde job failed"
			defer m.jobStorage.Delete(job.HordeJobID)
		case models.StatusRunning:
			swarmStatus = "running"
			message = "Horde job is running"
		default:
			continue
		}

		m.logger.Debug().Str("job_id", job.HordeJobID).Str("swarm_status", swarmStatus).Msg("Updating status in Swarm.")
		// Update Swarm
		if err := m.swarmServ.UpdateStatus(ctx, job.SwarmTest.UpdateURL,
			swarmStatus, []string{message}, job.HordeJobID); err != nil {
			m.logger.Error().Err(err).
				Str("job_id", job.HordeJobID).
				Msg("failed to update swarm status")
		}
	}
}
