package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/horde"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

func testHordeConfig(host string) *config.Config {
	return &config.Config{
		Horde: config.HordeConfig{
			Host:       host,
			APIKey:     "test-key",
			Timeout:    5,
			TemplateId: "test-template",
			StreamId:   "test-stream",
		},
		Retry: config.RetryConfig{
			MaxAttempts:  3,
			InitialDelay: 0,
			MaxDelay:     0,
		},
	}
}

func TestHordeService(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("CreateJob", func(t *testing.T) {
		var body map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/v1/jobs", r.URL.Path)
			assert.Equal(t, "ServiceAccount test-key", r.Header.Get("Authorization"))
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-job-id"}`))
		}))
		defer server.Close()

		service := NewHordeService(testHordeConfig(server.URL), logger)
		jobID, err := service.CreateJob(context.Background(), "12345")
		require.NoError(t, err)
		assert.Equal(t, "test-job-id", jobID)

		assert.Equal(t, "test-stream", body["streamId"])
		assert.Equal(t, "test-template", body["templateId"])
		assert.Equal(t, "Swarm preflight CL 12345", body["name"])
		assert.Equal(t, "12345", body["preflightCommitId"])
		assert.Equal(t, float64(12345), body["preflightChange"], "obsolete field sent as a number")
		assert.Equal(t, false, body["autoSubmit"])
	})

	t.Run("CreateJob accepts any 2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"created-id"}`))
		}))
		defer server.Close()

		jobID, err := NewHordeService(testHordeConfig(server.URL), logger).CreateJob(context.Background(), "1")
		require.NoError(t, err)
		assert.Equal(t, "created-id", jobID)
	})

	t.Run("client errors are not retried and include the body", func(t *testing.T) {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest} {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, `{"message":"nope"}`, status)
			}))

			_, err := NewHordeService(testHordeConfig(server.URL), logger).CreateJob(context.Background(), "1")
			server.Close()

			require.Error(t, err)
			assert.Contains(t, err.Error(), `{"message":"nope"}`)
			assert.Equal(t, int32(1), calls.Load(), "status %d must not be retried", status)
			switch status {
			case http.StatusUnauthorized:
				assert.Contains(t, err.Error(), "HORDE_API_KEY")
			case http.StatusForbidden:
				assert.Contains(t, err.Error(), "CreateJob")
			}
		}
	})

	t.Run("server errors are retried", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) < 3 {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"id":"after-retry"}`))
		}))
		defer server.Close()

		jobID, err := NewHordeService(testHordeConfig(server.URL), logger).CreateJob(context.Background(), "1")
		require.NoError(t, err)
		assert.Equal(t, "after-retry", jobID)
		assert.Equal(t, int32(3), calls.Load())
	})

	t.Run("GetJobStatus not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer server.Close()

		_, _, err := NewHordeService(testHordeConfig(server.URL), logger).GetJobStatus(context.Background(), "gone")
		require.Error(t, err)
		assert.True(t, horde.IsNotFound(err))
	})

	t.Run("GetJobStatus", func(t *testing.T) {
		tests := []struct {
			name     string
			jobState string
			batches  []horde.Batch
			want     models.JobStatus
		}{
			{
				name:     "running job",
				jobState: "Running",
				want:     models.StatusRunning,
			},
			{
				name:     "completed job",
				jobState: "Complete",
				batches:  []horde.Batch{{Error: "None"}}, // No errors indicate a successful completion
				want:     models.StatusCompleted,
			},
			{
				name:     "failed job",
				jobState: "Complete",
				batches:  []horde.Batch{{Error: "Some error occurred"}}, // Simulate failure with an error message
				want:     models.StatusFailed,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/v1/jobs/test-job-id", r.URL.Path)
					resp := horde.GetJobResponse{
						ID:      "test-job-id",
						State:   tt.jobState,
						Batches: tt.batches,
					}
					assert.NoError(t, json.NewEncoder(w).Encode(resp))
				}))
				defer server.Close()

				service := NewHordeService(testHordeConfig(server.URL), logger)
				status, _, err := service.GetJobStatus(context.Background(), "test-job-id")
				require.NoError(t, err)
				assert.Equal(t, tt.want, status)
			})
		}
	})
}

// TestEvaluateJob checks the mapping of raw Horde 5.8 job responses
func TestEvaluateJob(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		want       models.JobStatus
		wantReason string
	}{
		{
			name: "waiting",
			json: `{"id":"j","state":"Waiting","batches":[{"error":"None","steps":[{"state":"Waiting","outcome":"Unspecified"}]}]}`,
			want: models.StatusPending,
		},
		{
			name: "running",
			json: `{"id":"j","state":"Running","batches":[{"error":"None","steps":[{"state":"Running","outcome":"Unspecified"}]}]}`,
			want: models.StatusRunning,
		},
		{
			name: "complete with success and warnings",
			json: `{"id":"j","state":"Complete","batches":[{"error":"None","steps":[{"outcome":"Success"},{"outcome":"Warnings"}]}]}`,
			want: models.StatusCompleted,
		},
		{
			name:       "step failure",
			json:       `{"id":"j","state":"Complete","batches":[{"error":"None","steps":[{"name":"Compile Editor","outcome":"Failure"}]}]}`,
			want:       models.StatusFailed,
			wantReason: "Horde step 'Compile Editor' failed",
		},
		{
			name:       "SyncingFailed batch error",
			json:       `{"id":"j","state":"Complete","batches":[{"error":"SyncingFailed","steps":[]}]}`,
			want:       models.StatusFailed,
			wantReason: "Horde batch failed with error SyncingFailed",
		},
		{
			name: "missing batch error field is not a failure",
			json: `{"id":"j","state":"Running","batches":[{"steps":[]}]}`,
			want: models.StatusRunning,
		},
		{
			name:       "aborted by user info object",
			json:       `{"id":"j","state":"Complete","abortedByUserInfo":{"id":"u1","name":"Jane Doe","email":"jane@example.com"},"cancellationReason":"Superseded","batches":[]}`,
			want:       models.StatusFailed,
			wantReason: "Horde job was cancelled by Jane Doe: Superseded",
		},
		{
			name:       "aborted by deprecated string field",
			json:       `{"id":"j","state":"Complete","abortedByUser":"jdoe","batches":[]}`,
			want:       models.StatusFailed,
			wantReason: "Horde job was cancelled by jdoe",
		},
		{
			name: "null abort fields are not a cancellation",
			json: `{"id":"j","state":"Running","abortedByUser":null,"abortedByUserInfo":null,"cancellationReason":null,"batches":[]}`,
			want: models.StatusRunning,
		},
		{
			name:       "step aborted by user info",
			json:       `{"id":"j","state":"Complete","batches":[{"error":"None","steps":[{"name":"Cook","abortedByUserInfo":{"id":"u2","name":"Bob"}}]}]}`,
			want:       models.StatusFailed,
			wantReason: "Horde step 'Cook' was cancelled by Bob",
		},
		{
			name: "running job with an Incomplete batch is still running",
			json: `{"id":"j","state":"Running","batches":[{"error":"Incomplete","steps":[{"outcome":"Failure"}]},{"error":"None","steps":[{"state":"Running","outcome":"Unspecified"}]}]}`,
			want: models.StatusRunning,
		},
		{
			name: "failed step while running is not final (may be retried)",
			json: `{"id":"j","state":"Running","batches":[{"error":"None","steps":[{"name":"Compile","outcome":"Failure"},{"state":"Running"}]}]}`,
			want: models.StatusRunning,
		},
		{
			name: "fatal batch error while running is not final",
			json: `{"id":"j","state":"Running","batches":[{"error":"SyncingFailed","steps":[]}]}`,
			want: models.StatusRunning,
		},
		{
			name: "step aborted while running is not final",
			json: `{"id":"j","state":"Running","batches":[{"error":"None","steps":[{"name":"Cook","abortedByUserInfo":{"id":"u2","name":"Bob"}}]}]}`,
			want: models.StatusRunning,
		},
		{
			name:       "step aborted by legacy abortByUser field",
			json:       `{"id":"j","state":"Complete","batches":[{"error":"None","steps":[{"name":"Cook","abortByUser":"bob"}]}]}`,
			want:       models.StatusFailed,
			wantReason: "Horde step 'Cook' was cancelled by bob",
		},
		{
			name: "complete with Incomplete batch is not a failure",
			json: `{"id":"j","state":"Complete","batches":[{"error":"Incomplete","steps":[]},{"error":"None","steps":[{"outcome":"Success"}]}]}`,
			want: models.StatusCompleted,
		},
		{
			name: "complete with NoLongerNeeded batch is not a failure",
			json: `{"id":"j","state":"Complete","batches":[{"error":"NoLongerNeeded","steps":[{"outcome":"Failure"}]},{"error":"None","steps":[{"outcome":"Success"}]}]}`,
			want: models.StatusCompleted,
		},
		{
			name: "retried failed step is superseded by its retry",
			json: `{"id":"j","state":"Complete","batches":[{"error":"None","steps":[{"name":"Compile","outcome":"Failure","retriedByUserInfo":{"id":"u3","name":"Ann"}},{"name":"Compile","outcome":"Success"}]}]}`,
			want: models.StatusCompleted,
		},
		{
			name: "unknown state is not a failure",
			json: `{"id":"j","state":"SomethingNew","batches":[{"error":"None","steps":[]}]}`,
			want: models.StatusUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp horde.GetJobResponse
			require.NoError(t, json.Unmarshal([]byte(tt.json), &resp))

			status, reason := EvaluateJob(resp)
			assert.Equal(t, tt.want, status)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestNewCreateJobRequest(t *testing.T) {
	cfg := testHordeConfig("http://horde")

	req := NewCreateJobRequest(cfg, " 42 ")
	assert.Equal(t, "42", req.PreflightCommitId)
	assert.Equal(t, 42, req.PreflightChange)

	// Non numeric changes only use the string field
	req = NewCreateJobRequest(cfg, "abc")
	assert.Equal(t, "abc", req.PreflightCommitId)
	assert.Equal(t, 0, req.PreflightChange)
	data, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "preflightChange")
}

func TestJobURL(t *testing.T) {
	cfg := testHordeConfig("http://horde-server:5000")
	assert.Equal(t, "http://horde-server:5000/job/abc", NewHordeService(cfg, zerolog.Nop()).JobURL("abc"))

	cfg.Horde.PublicURL = "https://horde.example.com"
	assert.Equal(t, "https://horde.example.com/job/abc", NewHordeService(cfg, zerolog.Nop()).JobURL("abc"))
}

func TestIsFatalBatchError(t *testing.T) {
	for _, e := range []string{"", "None", "Incomplete", "NoLongerNeeded"} {
		assert.False(t, horde.IsFatalBatchError(e), e)
	}
	for _, e := range []string{"SyncingFailed", "UnknownError", "ExecutionError", "Cancelled", "SomethingNew"} {
		assert.True(t, horde.IsFatalBatchError(e), e)
	}
}
