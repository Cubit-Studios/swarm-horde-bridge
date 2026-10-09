package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/horde"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/services"
)

type JobMonitor struct {
	config     *config.Config
	logger     zerolog.Logger
	hordeServ  *services.HordeService
	swarmServ  *services.SwarmService
	jobStorage *services.JobStorage
	clock      config.Clock
}

func New(
	cfg *config.Config,
	logger zerolog.Logger,
	jobStorage *services.JobStorage,
	hordeServ *services.HordeService,
	swarmServ *services.SwarmService,
) *JobMonitor {
	clock := cfg.Clock
	if clock == nil {
		clock = config.RealClock{}
	}
	return &JobMonitor{
		clock:      clock,
		config:     cfg,
		logger:     logger,
		hordeServ:  hordeServ,
		swarmServ:  swarmServ,
		jobStorage: jobStorage,
	}
}

// Start polls Horde for every tracked job until ctx is cancelled. Jobs are
// checked once immediately so jobs restored from disk are resumed right away.
func (m *JobMonitor) Start(ctx context.Context) {
	m.logger.Debug().Msg("JobMonitor starting...")

	ticker := time.NewTicker(m.config.GetMonitorInterval())
	defer ticker.Stop()

	m.CheckJobs(ctx)

	for {
		select {
		case <-ctx.Done():
			m.logger.Debug().Msg("JobMonitor stopping due to context cancellation.")
			return
		case <-ticker.C:
			m.logger.Debug().Msg("JobMonitor tick - checking jobs...")
			m.CheckJobs(ctx)
		}
	}
}

// CheckJobs polls the status of every tracked job once and reports changes to Swarm
func (m *JobMonitor) CheckJobs(ctx context.Context) {
	jobs := m.jobStorage.List()
	m.logger.Debug().Int("job_count", len(jobs)).Msg("Checking job statuses...")

	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		m.checkJob(ctx, job)
	}
}

func (m *JobMonitor) checkJob(ctx context.Context, job *models.JobMapping) {
	log := m.logger.With().Str("job_id", job.HordeJobID).Str("change", job.SwarmTest.Changelist).Logger()
	log.Debug().Str("current_status", string(job.Status)).Msg("Checking job status...")

	switch {
	case job.PendingReport && isFinal(job.Status):
		// Final status already known, only the Swarm report is missing: retry it
	case m.clock.Now().Sub(job.CreatedAt) > m.config.GetMaxJobAge():
		m.setStatus(log, job, models.StatusFailed,
			fmt.Sprintf("Horde job did not finish within %s", m.config.GetMaxJobAge()))
	default:
		currentStatus, reason, err := m.hordeServ.GetJobStatus(ctx, job.HordeJobID)
		if err != nil {
			if !horde.IsNotFound(err) {
				log.Error().Err(err).Msg("failed to get job status")
				return
			}
			// The job was deleted from Horde: report it as failed so Swarm gets a final status
			currentStatus, reason = models.StatusFailed, "Horde job no longer exists"
		}
		if currentStatus != job.Status {
			m.setStatus(log, job, currentStatus, reason)
		}
	}

	if !job.PendingReport {
		log.Debug().Msg("No status change to report, skipping update.")
		return
	}

	swarmStatus, messages := swarmUpdateFor(job)
	log.Debug().Str("swarm_status", swarmStatus).Msg("Updating status in Swarm.")

	if err := m.swarmServ.UpdateStatus(ctx, job.SwarmTest.UpdateURL,
		swarmStatus, messages, m.hordeServ.JobURL(job.HordeJobID)); err != nil {
		// Keep PendingReport set: the update is retried on the next tick
		log.Error().Err(err).Msg("failed to update swarm status, will retry")
		return
	}

	if isFinal(job.Status) {
		if err := m.jobStorage.Delete(job.HordeJobID); err != nil {
			log.Error().Err(err).Msg("failed to persist job removal")
		}
		log.Info().Str("swarm_status", swarmStatus).Msg("Final status reported to Swarm, job no longer tracked.")
		return
	}

	job.PendingReport = false
	job.RunningReported = true
	if err := m.jobStorage.Store(job.HordeJobID, job); err != nil {
		log.Error().Err(err).Msg("failed to persist job status")
	}
}

// setStatus records a new job status, flagging it for a Swarm report when relevant
func (m *JobMonitor) setStatus(log zerolog.Logger, job *models.JobMapping, status models.JobStatus, reason string) {
	job.Status = status
	job.Reason = reason
	job.PendingReport = isReportable(job, status)
	if err := m.jobStorage.Store(job.HordeJobID, job); err != nil {
		log.Error().Err(err).Msg("failed to persist job status")
	}
	log.Info().Str("new_status", string(status)).Str("reason", reason).Msg("Job status updated.")
}

// isReportable reports whether a new status has to be sent to Swarm. Final
// statuses always are; "running" only the first time, because Horde reports
// Waiting between batches and every "running" update becomes a Swarm activity
// (and a Slack notification).
func isReportable(job *models.JobMapping, status models.JobStatus) bool {
	if isFinal(status) {
		return true
	}
	return status == models.StatusRunning && !job.RunningReported
}

// isFinal reports whether a status ends the tracking of a job
func isFinal(status models.JobStatus) bool {
	return status == models.StatusCompleted || status == models.StatusFailed
}

// swarmUpdateFor returns the Swarm status and messages for a job
func swarmUpdateFor(job *models.JobMapping) (string, []string) {
	switch job.Status {
	case models.StatusCompleted:
		return services.SwarmStatusPass, services.SwarmMessages("Horde job passed")
	case models.StatusFailed:
		return services.SwarmStatusFail, services.SwarmMessages("Horde job failed", job.Reason)
	default:
		return services.SwarmStatusRunning, services.SwarmMessages("Horde job is running")
	}
}
