package services

import (
	"testing"
	"time"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

func TestJobStorage(t *testing.T) {
	storage := NewJobStorage()

	// Test storing and retrieving a job
	t.Run("Store and Get", func(t *testing.T) {
		job := &models.JobMapping{
			SwarmTest: models.SwarmTestRequest{
				Changelist: "test-change",
			},
			HordeJobID: "job-123",
			Status:     models.StatusPending,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		storage.Store(job.HordeJobID, job)

		retrieved, exists := storage.Get(job.HordeJobID)
		if !exists {
			t.Error("Job not found after storing")
		}
		if retrieved.HordeJobID != job.HordeJobID {
			t.Errorf("Retrieved job ID = %v, want %v", retrieved.HordeJobID, job.HordeJobID)
		}
	})

	// Test listing jobs
	t.Run("List", func(t *testing.T) {
		storage = NewJobStorage() // Start fresh
		jobs := []*models.JobMapping{
			{
				HordeJobID: "job-1",
				Status:     models.StatusPending,
			},
			{
				HordeJobID: "job-2",
				Status:     models.StatusRunning,
			},
		}

		for _, job := range jobs {
			storage.Store(job.HordeJobID, job)
		}

		list := storage.List()
		if len(list) != len(jobs) {
			t.Errorf("List() returned %d jobs, want %d", len(list), len(jobs))
		}
	})

	// Test deleting a job
	t.Run("Delete", func(t *testing.T) {
		jobID := "job-to-delete"
		job := &models.JobMapping{
			HordeJobID: jobID,
			Status:     models.StatusPending,
		}

		storage.Store(jobID, job)
		storage.Delete(jobID)

		_, exists := storage.Get(jobID)
		if exists {
			t.Error("Job still exists after deletion")
		}
	})

	// Test cleaning old jobs
	t.Run("CleanOld", func(t *testing.T) {
		storage = NewJobStorage()

		// Create an old job timestamp
		oldTime := time.Now().Add(-2 * time.Hour)

		oldJob := &models.JobMapping{
			HordeJobID: "old-job",
			Status:     models.StatusPending,
			UpdatedAt:  oldTime,
		}

		newJob := &models.JobMapping{
			HordeJobID: "new-job",
			Status:     models.StatusPending,
			UpdatedAt:  time.Now(),
		}

		// Store jobs without updating timestamps
		storage.mu.Lock()
		storage.jobs[oldJob.HordeJobID] = oldJob
		storage.jobs[newJob.HordeJobID] = newJob
		storage.mu.Unlock()

		// Clean jobs older than 1 hour
		storage.CleanOld(1 * time.Hour)

		// Verify old job was removed
		if _, exists := storage.Get(oldJob.HordeJobID); exists {
			t.Error("Old job still exists after cleanup")
		}

		// Verify new job remains
		if _, exists := storage.Get(newJob.HordeJobID); !exists {
			t.Error("New job was incorrectly cleaned up")
		}
	})
}
