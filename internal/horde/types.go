// Package horde provides types and services for interacting with the Horde CI system
package horde

import (
	"encoding/json"
	"strings"
)

// Values used by the Horde API (verified against Horde 5.8)
const (
	// Job states (GetJobResponse.state)
	JobStateWaiting  = "Waiting"
	JobStateRunning  = "Running"
	JobStateComplete = "Complete"

	// Batch errors (GetBatchResponse.error). "AgentSetupFailed" is an obsolete
	// alias of "SyncingFailed" (same enum value). See IsFatalBatchError.
	BatchErrorNone           = "None"
	BatchErrorIncomplete     = "Incomplete"
	BatchErrorNoLongerNeeded = "NoLongerNeeded"
	BatchErrorSyncingFailed  = "SyncingFailed"

	// Step outcomes (GetStepResponse.outcome)
	OutcomeUnspecified = "Unspecified"
	OutcomeFailure     = "Failure"
	OutcomeWarnings    = "Warnings"
	OutcomeSuccess     = "Success"
)

// CreateJobRequest represents a job creation request to Horde (POST /api/v1/jobs)
type CreateJobRequest struct {
	StreamId   string `json:"streamId"`
	TemplateId string `json:"templateId"`
	Name       string `json:"name,omitempty"`
	// PreflightCommitId is the shelved change to test (Horde 5.8+ field)
	PreflightCommitId string `json:"preflightCommitId,omitempty"`
	// PreflightChange is obsolete in Horde 5.8 (mapped server side to
	// preflightCommitId) but still sent for older Horde versions. Both fields
	// carry the same change number, so sending both is harmless.
	PreflightChange int  `json:"preflightChange,omitempty"`
	AutoSubmit      bool `json:"autoSubmit"`
}

// CreateJobResponse represents a job creation response from Horde
type CreateJobResponse struct {
	ID string `json:"id"`
}

// UserInfo is Horde's "thin" user info object (GetThinUserInfoResponse)
type UserInfo struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// GetJobResponse represents a job status response from Horde (GET /api/v1/jobs/{id})
type GetJobResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	State string `json:"state"`
	// AbortedByUser is deprecated in favour of AbortedByUserInfo, parsed for older servers
	AbortedByUser     *string   `json:"abortedByUser,omitempty"`
	AbortedByUserInfo *UserInfo `json:"abortedByUserInfo,omitempty"`
	// CancellationReason is kept raw so an unexpected type never breaks decoding
	CancellationReason json.RawMessage `json:"cancellationReason,omitempty"`
	Batches            []Batch         `json:"batches"`
}

type Batch struct {
	Id    string `json:"id,omitempty"`
	State string `json:"state,omitempty"`
	Error string `json:"error"`
	Steps []Step `json:"steps"`
}

type Step struct {
	Id      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	State   string `json:"state"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
	// AbortByUser is the legacy (string) name of the user who aborted the step
	AbortByUser       *string   `json:"abortByUser,omitempty"`
	AbortedByUserInfo *UserInfo `json:"abortedByUserInfo,omitempty"`
	// RetryByUser (legacy string) / RetriedByUserInfo are set when the step was
	// retried: its outcome is then superseded by the retry
	RetryByUser       *string   `json:"retryByUser,omitempty"`
	RetriedByUserInfo *UserInfo `json:"retriedByUserInfo,omitempty"`
}

// AbortedBy reports whether the job was aborted by a user, and by whom
func (j GetJobResponse) AbortedBy() (string, bool) {
	return abortedBy(j.AbortedByUserInfo, j.AbortedByUser)
}

// AbortedBy reports whether the step was aborted by a user, and by whom
func (s Step) AbortedBy() (string, bool) {
	return abortedBy(s.AbortedByUserInfo, s.AbortByUser)
}

// WasRetried reports whether the step was retried (its outcome no longer counts)
func (s Step) WasRetried() bool {
	_, retried := abortedBy(s.RetriedByUserInfo, s.RetryByUser)
	return retried
}

// IsFatalBatchError mirrors Horde's own rule (JobCollection.IsFatalBatchError):
// a batch error is fatal unless it is None or Incomplete. NoLongerNeeded is also
// set on healthy jobs when work is superseded (e.g. a step was retried), so it
// is not fatal either. An empty value (field missing) is treated as None.
func IsFatalBatchError(e string) bool {
	switch e {
	case "", BatchErrorNone, BatchErrorIncomplete, BatchErrorNoLongerNeeded:
		return false
	}
	return true
}

func abortedBy(info *UserInfo, legacy *string) (string, bool) {
	if info != nil {
		switch {
		case info.Name != "":
			return info.Name, true
		case info.Id != "":
			return info.Id, true
		default:
			return "unknown user", true
		}
	}
	if legacy != nil && *legacy != "" {
		return *legacy, true
	}
	return "", false
}

// CancellationReasonText returns the cancellation reason as text, or "" if none
func (j GetJobResponse) CancellationReasonText() string {
	raw := strings.TrimSpace(string(j.CancellationReason))
	if raw == "" || raw == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(j.CancellationReason, &s); err == nil {
		return s
	}
	return raw
}
