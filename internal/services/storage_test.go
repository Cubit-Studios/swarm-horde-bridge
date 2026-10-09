package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

func TestJobStorage(t *testing.T) {
	storage := NewJobStorage(nil)

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

		require.NoError(t, storage.Store(job.HordeJobID, job))

		retrieved, exists := storage.Get(job.HordeJobID)
		require.True(t, exists, "Job not found after storing")
		assert.Equal(t, job.HordeJobID, retrieved.HordeJobID)

		// Returned mappings are copies
		retrieved.Status = models.StatusFailed
		again, _ := storage.Get(job.HordeJobID)
		assert.Equal(t, models.StatusPending, again.Status)
	})

	// Test listing jobs
	t.Run("List", func(t *testing.T) {
		storage = NewJobStorage(nil) // Start fresh
		now := time.Now()
		jobs := []*models.JobMapping{
			{
				HordeJobID: "job-2",
				Status:     models.StatusRunning,
				CreatedAt:  now,
			},
			{
				HordeJobID: "job-1",
				Status:     models.StatusPending,
				CreatedAt:  now.Add(-time.Minute),
			},
		}

		for _, job := range jobs {
			require.NoError(t, storage.Store(job.HordeJobID, job))
		}

		list := storage.List()
		require.Len(t, list, len(jobs))
		assert.Equal(t, "job-1", list[0].HordeJobID, "oldest first")
	})

	// Test deleting a job
	t.Run("Delete", func(t *testing.T) {
		jobID := "job-to-delete"
		job := &models.JobMapping{
			HordeJobID: jobID,
			Status:     models.StatusPending,
		}

		require.NoError(t, storage.Store(jobID, job))
		require.NoError(t, storage.Delete(jobID))

		_, exists := storage.Get(jobID)
		assert.False(t, exists, "Job still exists after deletion")
		assert.NoError(t, storage.Delete("unknown"))
	})

}

func TestFileJobStorage(t *testing.T) {
	t.Run("creates data dir and empty store", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nested", "data")

		storage, err := NewFileJobStorage(dir, nil, zerolog.Nop())
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, JobsFileName), storage.Path())
		assert.Empty(t, storage.List())

		data, err := os.ReadFile(storage.Path())
		require.NoError(t, err)
		assert.Contains(t, string(data), `"version": 1`)
	})

	t.Run("jobs survive a restart", func(t *testing.T) {
		dir := t.TempDir()
		created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

		storage, err := NewFileJobStorage(dir, nil, zerolog.Nop())
		require.NoError(t, err)

		require.NoError(t, storage.Store("job-1", &models.JobMapping{
			SwarmTest: models.SwarmTestRequest{
				Changelist: "1234",
				UpdateURL:  "https://swarm.example.com/api/v10/testruns/1/abc",
			},
			HordeJobID:    "job-1",
			Status:        models.StatusFailed,
			Reason:        "Horde step 'Cook' failed",
			PendingReport: true,
			CreatedAt:     created,
		}))
		require.NoError(t, storage.Store("job-2", &models.JobMapping{HordeJobID: "job-2", Status: models.StatusRunning}))
		require.NoError(t, storage.Delete("job-2"))

		// Simulate a restart
		reloaded, err := NewFileJobStorage(dir, nil, zerolog.Nop())
		require.NoError(t, err)

		list := reloaded.List()
		require.Len(t, list, 1)
		job := list[0]
		assert.Equal(t, "job-1", job.HordeJobID)
		assert.Equal(t, "1234", job.SwarmTest.Changelist)
		assert.Equal(t, "https://swarm.example.com/api/v10/testruns/1/abc", job.SwarmTest.UpdateURL)
		assert.Equal(t, models.StatusFailed, job.Status)
		assert.Equal(t, "Horde step 'Cook' failed", job.Reason)
		assert.True(t, job.PendingReport)
		assert.True(t, created.Equal(job.CreatedAt))
	})

	t.Run("no temporary files left behind", func(t *testing.T) {
		dir := t.TempDir()
		storage, err := NewFileJobStorage(dir, nil, zerolog.Nop())
		require.NoError(t, err)
		for i := 0; i < 5; i++ {
			require.NoError(t, storage.Store("job", &models.JobMapping{HordeJobID: "job"}))
		}

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, JobsFileName, entries[0].Name())
	})

	t.Run("corrupt file is moved aside and the store starts empty", func(t *testing.T) {
		dir := t.TempDir()
		clock := fixedClock{t: time.Unix(1700000000, 0)}
		require.NoError(t, os.WriteFile(filepath.Join(dir, JobsFileName), []byte("{not json"), 0o600))

		storage, err := NewFileJobStorage(dir, clock, zerolog.Nop())
		require.NoError(t, err)
		assert.Empty(t, storage.List())

		moved, err := os.ReadFile(filepath.Join(dir, JobsFileName+".corrupt-1700000000"))
		require.NoError(t, err, "corrupt file must be kept for inspection")
		assert.Equal(t, "{not json", string(moved))

		// A fresh, valid store was written and is usable
		require.NoError(t, storage.Store("job", &models.JobMapping{HordeJobID: "job"}))
		reloaded, err := NewFileJobStorage(dir, clock, zerolog.Nop())
		require.NoError(t, err)
		assert.Len(t, reloaded.List(), 1)
	})

	t.Run("UpdatedAt comes from the clock", func(t *testing.T) {
		clock := fixedClock{t: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
		storage := NewJobStorage(clock)
		require.NoError(t, storage.Store("job", &models.JobMapping{HordeJobID: "job"}))
		job, _ := storage.Get("job")
		assert.True(t, clock.t.Equal(job.UpdatedAt))
	})
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var _ config.Clock = fixedClock{}
