// internal/services/swarm_test.go
package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/rs/zerolog"
)

func TestSwarmService(t *testing.T) {
	logger := zerolog.New(nil)

	t.Run("UpdateStatus", func(t *testing.T) {
		tests := []struct {
			name           string
			status         string
			messages       []string
			serverResponse int
			wantErr        bool
			checkRequest   func(*testing.T, *http.Request)
		}{
			{
				name:           "successful update",
				status:         "running",
				messages:       []string{"Test message"},
				serverResponse: http.StatusOK,
				wantErr:        false,
				checkRequest: func(t *testing.T, r *http.Request) {
					// Verify request method
					if r.Method != "POST" {
						t.Errorf("Expected POST request, got %s", r.Method)
					}

					// Verify Content-Type
					contentType := r.Header.Get("Content-Type")
					if contentType != "application/json" {
						t.Errorf("Expected Content-Type application/json, got %s", contentType)
					}

					// Verify request body
					var update models.SwarmUpdateRequest
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Fatalf("Failed to decode request body: %v", err)
					}

					if update.Status != "running" {
						t.Errorf("Expected status 'running', got %s", update.Status)
					}

					if len(update.Messages) != 1 || update.Messages[0] != "Test message" {
						t.Errorf("Expected messages ['Test message'], got %v", update.Messages)
					}
				},
			},
			{
				name:           "server error",
				status:         "failed",
				messages:       []string{"Error message"},
				serverResponse: http.StatusInternalServerError,
				wantErr:        true,
				checkRequest:   func(t *testing.T, r *http.Request) {},
			},
			{
				name:           "empty messages",
				status:         "completed",
				messages:       []string{},
				serverResponse: http.StatusOK,
				wantErr:        false,
				checkRequest: func(t *testing.T, r *http.Request) {
					var update models.SwarmUpdateRequest
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Fatalf("Failed to decode request body: %v", err)
					}

					if len(update.Messages) != 0 {
						t.Errorf("Expected empty messages, got %v", update.Messages)
					}
				},
			},
			{
				name:           "multiple messages",
				status:         "running",
				messages:       []string{"Message 1", "Message 2", "Message 3"},
				serverResponse: http.StatusOK,
				wantErr:        false,
				checkRequest: func(t *testing.T, r *http.Request) {
					var update models.SwarmUpdateRequest
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Fatalf("Failed to decode request body: %v", err)
					}

					if len(update.Messages) != 3 {
						t.Errorf("Expected 3 messages, got %d", len(update.Messages))
					}
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					tt.checkRequest(t, r)
					w.WriteHeader(tt.serverResponse)
				}))
				defer server.Close()

				cfg := &config.Config{
					Swarm: config.SwarmConfig{
						Host: server.URL,
					},
				}

				service := NewSwarmService(cfg, logger)
				err := service.UpdateStatus(context.Background(), server.URL+"/update", tt.status, tt.messages, "")

				if (err != nil) != tt.wantErr {
					t.Errorf("UpdateStatus() error = %v, wantErr %v", err, tt.wantErr)
				}
			})
		}
	})

	t.Run("Context Cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate a slow response
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		cfg := &config.Config{
			Swarm: config.SwarmConfig{
				Host: server.URL,
			},
		}

		service := NewSwarmService(cfg, logger)

		// Create a context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := service.UpdateStatus(ctx, server.URL+"/update", "running", []string{"test"}, "")
		if err == nil {
			t.Error("Expected error due to context cancellation, got nil")
		}
	})

	t.Run("Invalid URL", func(t *testing.T) {
		cfg := &config.Config{
			Swarm: config.SwarmConfig{
				Host: "http://invalid-url",
			},
		}

		service := NewSwarmService(cfg, logger)
		err := service.UpdateStatus(context.Background(), "://invalid-url", "running", []string{"test"}, "")
		if err == nil {
			t.Error("Expected error for invalid URL, got nil")
		}
	})

	t.Run("Request Timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		cfg := &config.Config{
			Swarm: config.SwarmConfig{
				Host: server.URL,
			},
		}

		service := NewSwarmService(cfg, logger)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := service.UpdateStatus(ctx, server.URL+"/update", "running", []string{"test"}, "")
		if err == nil {
			t.Error("Expected timeout error, got nil")
		}
	})
}

// Helper function to compare JSON request bodies
func jsonEqual(t *testing.T, body []byte, expected models.SwarmUpdateRequest) bool {
	t.Helper()
	var actual models.SwarmUpdateRequest
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("Failed to unmarshal actual JSON: %v", err)
		return false
	}

	return actual.Status == expected.Status &&
		len(actual.Messages) == len(expected.Messages) &&
		messagesEqual(actual.Messages, expected.Messages)
}

func messagesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
