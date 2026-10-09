package models

import "time"

// HordeJobStatus represents the possible states of a Horde job
type HordeJobStatus string

const (
	JobStatusPending   HordeJobStatus = "pending"
	JobStatusRunning   HordeJobStatus = "running"
	JobStatusCompleted HordeJobStatus = "completed"
	JobStatusFailed    HordeJobStatus = "failed"
)

// JobStatus represents the possible states of a job
type JobStatus string

const (
	StatusUnknown   JobStatus = "unknown"
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
)

type SwarmTestRequest struct {
	Changelist string `json:"changelist"`
	UpdateURL  string `json:"update_url"`
}

type SwarmUpdateRequest struct {
	Status   string   `json:"status"`
	JobUrl   string   `json:"url"`
	Messages []string `json:"messages"`
}

type JobMapping struct {
	SwarmTest  SwarmTestRequest `json:"swarm_test"`
	HordeJobID string           `json:"horde_job_id"`
	Status     JobStatus        `json:"status"`
	// Reason explains a failed status (sent to Swarm)
	Reason string `json:"reason,omitempty"`
	// PendingReport is true while the current Status still has to be
	// delivered to Swarm (e.g. the previous update attempt failed)
	PendingReport bool `json:"pending_report,omitempty"`
	// RunningReported is true once Swarm has been told the job is running.
	// Horde flips between Waiting and Running between batches; Swarm (and
	// anything listening to its activity, e.g. Slack) must hear "running" once.
	RunningReported bool      `json:"running_reported,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// HordeCreateJobRequest represents a job creation request to Horde
type HordeCreateJobRequest struct {
	Template string            `json:"template"`
	Change   string            `json:"change"`
	Params   map[string]string `json:"params,omitempty"`
}
