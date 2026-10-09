// internal/services/swarm_test.go
package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
					Swarm: config.SwarmConfig{Timeout: 5},
				}

				service := NewSwarmService(cfg, logger)
				err := service.UpdateStatus(context.Background(), server.URL+"/update", tt.status, tt.messages, "")

				if (err != nil) != tt.wantErr {
					t.Errorf("UpdateStatus() error = %v, wantErr %v", err, tt.wantErr)
				}
			})
		}
	})

	t.Run("Any 2xx is accepted and job URL is sent", func(t *testing.T) {
		var got models.SwarmUpdateRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		cfg := &config.Config{Swarm: config.SwarmConfig{Timeout: 5}}
		service := NewSwarmService(cfg, logger)
		err := service.UpdateStatus(context.Background(), server.URL+"/update", "pass", []string{"ok"}, "https://horde.example.com/job/abc")
		if err != nil {
			t.Fatalf("UpdateStatus() error = %v", err)
		}
		if got.JobUrl != "https://horde.example.com/job/abc" {
			t.Errorf("Expected job url to be sent, got %q", got.JobUrl)
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
			Swarm: config.SwarmConfig{Timeout: 5},
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
			Swarm: config.SwarmConfig{Timeout: 5},
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
			Swarm: config.SwarmConfig{Timeout: 5},
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

func TestSwarmMessages(t *testing.T) {
	long := strings.Repeat("x", 200)

	msgs := SwarmMessages("Horde job failed", long)
	if msgs[0] != "Horde job failed" {
		t.Errorf("first message must be the summary, got %q", msgs[0])
	}
	if len(msgs) != 4 { // summary + 80 + 80 + 40
		t.Fatalf("expected 4 messages, got %d: %v", len(msgs), msgs)
	}
	if strings.Join(msgs[1:], "") != long {
		t.Error("details must be preserved across messages")
	}

	msgs = SwarmMessages(strings.Repeat("s", 100), strings.Repeat("d", 2000))
	if len(msgs) != SwarmMaxMessages {
		t.Errorf("expected at most %d messages, got %d", SwarmMaxMessages, len(msgs))
	}
	for _, m := range msgs {
		if n := len([]rune(m)); n > SwarmMaxMessageLength {
			t.Errorf("message longer than %d chars: %d", SwarmMaxMessageLength, n)
		}
	}

	if got := SwarmMessages("ok"); len(got) != 1 {
		t.Errorf("expected only the summary, got %v", got)
	}
}

func TestUpdateStatusEnforcesSwarmLimits(t *testing.T) {
	var got models.SwarmUpdateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer server.Close()

	messages := make([]string, 15)
	for i := range messages {
		messages[i] = strings.Repeat("é", 100)
	}
	service := NewSwarmService(&config.Config{Swarm: config.SwarmConfig{Timeout: 5}}, zerolog.Nop())
	if err := service.UpdateStatus(context.Background(), server.URL, "fail", messages, ""); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if len(got.Messages) != SwarmMaxMessages {
		t.Errorf("expected %d messages, got %d", SwarmMaxMessages, len(got.Messages))
	}
	for _, m := range got.Messages {
		if n := len([]rune(m)); n > SwarmMaxMessageLength {
			t.Errorf("message longer than %d chars: %d", SwarmMaxMessageLength, n)
		}
	}
}

func TestCheckUpdateURL(t *testing.T) {
	tests := []struct {
		url     string
		allowed string
		wantErr bool
	}{
		{"https://swarm.example.com/api/v10/testruns/1/x", "", false},
		{"http://anything/x", "", false},
		{"ftp://swarm.example.com/x", "", true},
		{"https://user:pw@swarm.example.com/x", "", true},
		{"not a url", "", true},
		{"https://swarm.example.com/api/x", "swarm.example.com", false},
		{"https://SWARM.example.com:443/api/x", "swarm.example.com", false},
		{"http://swarm.example.com/api/x", "swarm.example.com", true},
		{"https://swarm.example.com:8443/api/x", "swarm.example.com", true},
		{"https://swarm.example.com.evil.com/api/x", "swarm.example.com", true},
		{"https://evil.com/api/x", "swarm.example.com", true},
		{"https://swarm.example.com:8443/api/x", "swarm.example.com:8443", false},
		{"https://swarm.example.com/api/x", "swarm.example.com:8443", true},
		{"http://127.0.0.1:9000/x", "127.0.0.1:9000", false},
		{"http://localhost/x", "localhost", false},
		{"http://127.0.0.1:9001/x", "127.0.0.1:9000", true},
	}
	for _, tt := range tests {
		err := CheckUpdateURL(tt.url, tt.allowed)
		if (err != nil) != tt.wantErr {
			t.Errorf("CheckUpdateURL(%q, %q) error = %v, wantErr %v", tt.url, tt.allowed, err, tt.wantErr)
		}
	}
}

func TestUpdateStatusErrorsDoNotLeakUpdateURL(t *testing.T) {
	service := NewSwarmService(&config.Config{Swarm: config.SwarmConfig{Timeout: 1}}, zerolog.Nop())
	err := service.UpdateStatus(context.Background(), "http://127.0.0.1:1/api/v10/testruns/1/secret-token", "running", nil, "")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("error leaks the update URL: %v", err)
	}
}
