package horde

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Client handles communication with the Horde API
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	logger     zerolog.Logger
}

// ClientOption allows customizing the Client during initialization
type ClientOption func(*Client)

// WithTimeout sets the HTTP client timeout
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// NewClient creates a new Horde API client
func NewClient(baseURL, apiKey string, logger zerolog.Logger, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// CreateJob creates a new job in Horde
func (c *Client) CreateJob(ctx context.Context, req CreateJobRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/jobs", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Authorization", fmt.Sprintf("ServiceAccount %s", c.apiKey))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var jobResp CreateJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	return jobResp.ID, nil
}

// GetJobStatus retrieves the current status of a job
func (c *Client) GetJobStatus(ctx context.Context, jobID string) (GetJobResponse, error) {
	url := fmt.Sprintf("%s/api/v1/jobs/%s", c.baseURL, jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return GetJobResponse{}, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("ServiceAccount %s", c.apiKey))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return GetJobResponse{}, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return GetJobResponse{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var jobResp GetJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobResp); err != nil {
		return GetJobResponse{}, fmt.Errorf("decoding response: %w", err)
	}

	return jobResp, nil
}
