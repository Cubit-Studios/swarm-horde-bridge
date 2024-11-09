package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/horde"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/rs/zerolog"
)

func TestHordeService(t *testing.T) {
	logger := zerolog.New(nil)

	t.Run("CreateJob", func(t *testing.T) {
		// Create test server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.Info().Msgf("Received request")
			// Verify request
			if r.Method != "POST" {
				t.Errorf("Expected POST request, got %s", r.Method)
			}
			if r.Header.Get("Authorization") != "ServiceAccount test-key" {
				t.Errorf("Expected Authorization header, got %s", r.Header.Get("Authorization"))
			}

			// Return test response
			resp := horde.GetJobResponse{
				ID:    "test-job-id",
				State: "Pending",
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		cfg := &config.Config{
			Horde: config.HordeConfig{
				Host:   server.URL,
				APIKey: "test-key",
			},
			Retry: config.RetryConfig{
				MaxAttempts:  3,
				InitialDelay: 1,
				MaxDelay:     5,
			},
		}

		service := NewHordeService(cfg, logger)
		jobID, err := service.CreateJob(context.Background(), "test-change")
		if err != nil {
			t.Fatalf("CreateJob() error = %v", err)
		}
		if jobID != "test-job-id" {
			t.Errorf("CreateJob() = %v, want %v", jobID, "test-job-id")
		}
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
					resp := horde.GetJobResponse{
						ID:      "test-job-id",
						State:   tt.jobState,
						Batches: tt.batches,
					}
					json.NewEncoder(w).Encode(resp)
				}))
				defer server.Close()

				cfg := &config.Config{
					Horde: config.HordeConfig{
						Host:   server.URL,
						APIKey: "test-key",
					},
					Retry: config.RetryConfig{
						MaxAttempts:  3,
						InitialDelay: 1,
						MaxDelay:     5,
					},
				}

				service := NewHordeService(cfg, logger)
				status, err := service.GetJobStatus(context.Background(), "test-job-id")
				if err != nil {
					t.Fatalf("GetJobStatus() error = %v", err)
				}
				if status != tt.want {
					t.Errorf("GetJobStatus() = %v, want %v", status, tt.want)
				}
			})
		}
	})
}
