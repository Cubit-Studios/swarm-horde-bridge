package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

type SwarmService struct {
	client *http.Client
	config *config.Config
	logger zerolog.Logger
}

func NewSwarmService(cfg *config.Config, logger zerolog.Logger) *SwarmService {
	return &SwarmService{
		client: &http.Client{},
		config: cfg,
		logger: logger,
	}
}

func (s *SwarmService) UpdateStatus(ctx context.Context, updateURL string, status string, messages []string, jobID string) error {
	// Construct the JobUrl using the Horde URL and Job ID
	jobURL := fmt.Sprintf("%s/job/%s", s.config.Horde.Host, jobID)

	update := models.SwarmUpdateRequest{
		Status:   status,
		Messages: messages,
		JobUrl:   jobURL,
	}

	body, err := json.Marshal(update)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", updateURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return nil
}
