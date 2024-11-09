package services

import (
	"sync"
	"time"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

// JobStorage provides thread-safe storage for job mappings
type JobStorage struct {
	mu   sync.RWMutex
	jobs map[string]*models.JobMapping
}

// NewJobStorage creates a new job storage instance
func NewJobStorage() *JobStorage {
	return &JobStorage{
		jobs: make(map[string]*models.JobMapping),
	}
}

// Store saves a job mapping
func (s *JobStorage) Store(jobID string, mapping *models.JobMapping) {
	s.mu.Lock()
	defer s.mu.Unlock()

	mapping.UpdatedAt = time.Now()
	s.jobs[jobID] = mapping
}

// Get retrieves a job mapping
func (s *JobStorage) Get(jobID string) (*models.JobMapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mapping, exists := s.jobs[jobID]
	return mapping, exists
}

// Delete removes a job mapping
func (s *JobStorage) Delete(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.jobs, jobID)
}

// List returns all job mappings
func (s *JobStorage) List() []*models.JobMapping {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mappings := make([]*models.JobMapping, 0, len(s.jobs))
	for _, mapping := range s.jobs {
		mappings = append(mappings, mapping)
	}
	return mappings
}

// CleanOld removes jobs older than the specified duration
func (s *JobStorage) CleanOld(age time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-age)
	for id, job := range s.jobs {
		if job.UpdatedAt.Before(cutoff) {
			delete(s.jobs, id)
		}
	}
}
