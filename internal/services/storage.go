package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

// JobsFileName is the name of the job store file inside the data directory
const JobsFileName = "jobs.json"

// jobsFileVersion is the format version of the job store file
const jobsFileVersion = 1

// jobsFile is the on-disk representation of the job store
type jobsFile struct {
	Version int                           `json:"version"`
	Jobs    map[string]*models.JobMapping `json:"jobs"`
}

// JobStorage provides thread-safe storage for job mappings.
//
// When created with NewFileJobStorage, every change is written atomically
// (temp file + rename) to a JSON file so in-flight jobs survive restarts.
type JobStorage struct {
	mu    sync.RWMutex
	jobs  map[string]*models.JobMapping
	clock config.Clock
	// path of the JSON file, empty for an in-memory only store
	path string
}

// NewJobStorage creates a new in-memory job storage instance.
// A nil clock defaults to the real clock.
func NewJobStorage(clock config.Clock) *JobStorage {
	if clock == nil {
		clock = config.RealClock{}
	}
	return &JobStorage{
		jobs:  make(map[string]*models.JobMapping),
		clock: clock,
	}
}

// NewFileJobStorage creates a job storage persisted as JSON in dataDir,
// loading any jobs saved by a previous run. A corrupt store file is renamed
// to jobs.json.corrupt-<unix time> and the store starts empty (the error is
// logged), so the service can still start without manual intervention.
func NewFileJobStorage(dataDir string, clock config.Clock, logger zerolog.Logger) (*JobStorage, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	s := NewJobStorage(clock)
	s.path = filepath.Join(dataDir, JobsFileName)

	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// First start: write an empty store now so permission problems on
		// the data directory are reported at startup, not on the first job
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("reading job store: %w", err)
	}

	var file jobsFile
	if err := json.Unmarshal(data, &file); err != nil {
		corrupt := fmt.Sprintf("%s.corrupt-%d", s.path, s.clock.Now().Unix())
		if rerr := os.Rename(s.path, corrupt); rerr != nil {
			return nil, fmt.Errorf("parsing job store %s: %w (and moving it aside failed: %v)", s.path, err, rerr)
		}
		logger.Error().Err(err).Str("path", s.path).Str("moved_to", corrupt).
			Msg("job store is corrupt, moved it aside and starting with an empty store; tracked jobs were lost")
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
		return s, nil
	}
	for id, job := range file.Jobs {
		if job != nil {
			s.jobs[id] = job
		}
	}
	return s, nil
}

// Path returns the file the store is persisted to, empty for an in-memory store
func (s *JobStorage) Path() string {
	return s.path
}

// Store saves a job mapping. The in-memory store is always updated; the
// returned error reports a failure to persist it.
func (s *JobStorage) Store(jobID string, mapping *models.JobMapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	m := *mapping
	m.UpdatedAt = s.clock.Now()
	mapping.UpdatedAt = m.UpdatedAt
	s.jobs[jobID] = &m
	return s.persistLocked()
}

// Get retrieves a copy of a job mapping
func (s *JobStorage) Get(jobID string) (*models.JobMapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mapping, exists := s.jobs[jobID]
	if !exists {
		return nil, false
	}
	m := *mapping
	return &m, true
}

// Delete removes a job mapping
func (s *JobStorage) Delete(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[jobID]; !exists {
		return nil
	}
	delete(s.jobs, jobID)
	return s.persistLocked()
}

// List returns copies of all job mappings, oldest first
func (s *JobStorage) List() []*models.JobMapping {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mappings := make([]*models.JobMapping, 0, len(s.jobs))
	for _, mapping := range s.jobs {
		m := *mapping
		mappings = append(mappings, &m)
	}
	sort.Slice(mappings, func(i, j int) bool {
		return mappings[i].CreatedAt.Before(mappings[j].CreatedAt)
	})
	return mappings
}

// persistLocked atomically writes the store to disk. Callers must hold s.mu.
func (s *JobStorage) persistLocked() error {
	if s.path == "" {
		return nil
	}

	data, err := json.MarshalIndent(jobsFile{Version: jobsFileVersion, Jobs: s.jobs}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding job store: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), JobsFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary job store file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing job store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing job store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing job store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replacing job store: %w", err)
	}
	return nil
}
