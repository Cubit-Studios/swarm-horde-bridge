// Package horde provides types and services for interacting with the Horde CI system
package horde

// Common status and outcome types for Horde jobs
const (
	// Job states
	StateInitializing = "Initializing"
	StateQueued       = "Queued"
	StateRunning      = "Running"
	StateFinished     = "Finished"
	StateFailed       = "Failed"
	StateCanceled     = "Canceled"

	// Job outcomes
	OutcomeSucceeded = "Succeeded"
	OutcomeFailed    = "Failed"
	OutcomeCanceled  = "Canceled"
)

// CreateJobRequest represents a job creation request to Horde
type CreateJobRequest struct {
	StreamId        string `json:"streamId"`
	TemplateId      string `json:"templateId"`
	Name            string `json:"name"`
	PreflightChange string `json:"preflightChange"`
	AutoSubmit      bool   `json:"autoSubmit"`
}

// CreateJobResponse represents a job creation response from Horde
type CreateJobResponse struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// GetJobResponse represents a job status response from Horde
type GetJobResponse struct {
	ID              string  `json:"id"`
	State           string  `json:"state"`
	AbortedByUserId *string `json:"abortedByUser"`
	Batches         []Batch `json:"batches"`
}

type Batch struct {
	Error string `json:"error"`
	Steps []Step `json:"steps"`
}

type Step struct {
	AbortedByUserId *string `json:"abortedByUserId"`
	State           string  `json:"state"`
	Outcome         string  `json:"outcome"`
	Error           string  `json:"error"`
}
