package horde

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// maxErrorBodyLen limits how much of an error response body is kept in errors/logs
const maxErrorBodyLen = 2048

// maxResponseSize limits how much of a response body is read
const maxResponseSize = 10 << 20

// APIError is returned when Horde answers with a non-2xx status code
type APIError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("horde API %s %s returned HTTP %d", e.Method, e.URL, e.StatusCode)
	if hint := e.hint(); hint != "" {
		msg += " (" + hint + ")"
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

func (e *APIError) hint() string {
	switch {
	case e.StatusCode == http.StatusUnauthorized:
		return "unauthorized: check the service account token in HORDE_API_KEY"
	case e.StatusCode == http.StatusForbidden:
		return "forbidden: the service account needs CreateJob on the stream and template ACLs, and ViewJob to read jobs"
	case e.StatusCode == http.StatusNotFound:
		return "not found"
	case e.StatusCode >= 300 && e.StatusCode < 400:
		return "unexpected redirect: check HORDE_HOST (scheme/host) and the service account token"
	}
	return ""
}

// Retryable reports whether retrying the request may succeed
func (e *APIError) Retryable() bool {
	return e.StatusCode >= 500 ||
		e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode == http.StatusTooManyRequests
}

// IsNotFound reports whether err is a Horde 404 error
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsRetryable reports whether a request that failed with err may succeed if retried.
// Transport errors are retryable, as are 5xx/408/429 responses.
func IsRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}
	return true
}

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
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			// Do not follow redirects: Horde redirects unauthenticated browser
			// requests to its login page, which would hide auth problems and
			// turn POSTs into GETs.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger: logger,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// CreateJob creates a new job in Horde and returns its id
func (c *Client) CreateJob(ctx context.Context, req CreateJobRequest) (string, error) {
	var jobResp CreateJobResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/jobs", req, &jobResp); err != nil {
		return "", err
	}
	if jobResp.ID == "" {
		return "", fmt.Errorf("horde returned an empty job id")
	}
	return jobResp.ID, nil
}

// GetJobStatus retrieves the current status of a job
func (c *Client) GetJobStatus(ctx context.Context, jobID string) (GetJobResponse, error) {
	var jobResp GetJobResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(jobID), nil, &jobResp); err != nil {
		return GetJobResponse{}, err
	}
	return jobResp, nil
}

// do sends a request to the Horde API and decodes the JSON response into out
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	endpoint := c.baseURL + path

	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "ServiceAccount "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{
			Method:     method,
			URL:        endpoint,
			StatusCode: resp.StatusCode,
			Body:       truncate(strings.TrimSpace(string(data)), maxErrorBodyLen),
		}
		if loc := resp.Header.Get("Location"); loc != "" && apiErr.Body == "" {
			apiErr.Body = "redirected to " + loc
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			c.logger.Error().
				Int("status", resp.StatusCode).
				Str("method", method).
				Str("url", endpoint).
				Msg("Horde rejected the request: " + apiErr.hint())
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding response (HTTP %d): %w: %s", resp.StatusCode, err, truncate(string(data), maxErrorBodyLen))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
