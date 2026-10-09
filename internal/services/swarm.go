package services

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

	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
)

// Swarm test run statuses accepted by the Swarm update URL
const (
	SwarmStatusRunning = "running"
	SwarmStatusPass    = "pass"
	SwarmStatusFail    = "fail"
)

type SwarmService struct {
	client *http.Client
	config *config.Config
	logger zerolog.Logger
}

func NewSwarmService(cfg *config.Config, logger zerolog.Logger) *SwarmService {
	return &SwarmService{
		client: &http.Client{Timeout: cfg.GetSwarmTimeout()},
		config: cfg,
		logger: logger,
	}
}

// UpdateStatus posts a test run status to the update URL supplied by Swarm.
// jobURL is the link shown in Swarm (may be empty).
func (s *SwarmService) UpdateStatus(ctx context.Context, updateURL string, status string, messages []string, jobURL string) error {
	update := models.SwarmUpdateRequest{
		Status:   status,
		Messages: limitMessages(messages),
		JobUrl:   jobURL,
	}

	body, err := json.Marshal(update)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, updateURL, bytes.NewReader(body))
	if err != nil {
		return redactErr(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return redactErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("unexpected status from swarm: %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

// Swarm only displays short test run messages
const (
	SwarmMaxMessages      = 10
	SwarmMaxMessageLength = 80
)

// SwarmMessages builds the messages of a Swarm status update: a short summary
// first, then the details split into chunks Swarm can display. Every message
// is at most SwarmMaxMessageLength characters and at most SwarmMaxMessages
// messages are returned.
func SwarmMessages(summary string, details ...string) []string {
	messages := []string{truncateRunes(summary, SwarmMaxMessageLength)}
	for _, detail := range details {
		for _, chunk := range splitRunes(strings.TrimSpace(detail), SwarmMaxMessageLength) {
			if len(messages) == SwarmMaxMessages {
				return messages
			}
			messages = append(messages, chunk)
		}
	}
	return messages
}

// limitMessages enforces Swarm's message count and length limits
func limitMessages(messages []string) []string {
	if len(messages) > SwarmMaxMessages {
		messages = messages[:SwarmMaxMessages]
	}
	out := make([]string, len(messages))
	for i, m := range messages {
		out[i] = truncateRunes(m, SwarmMaxMessageLength)
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func splitRunes(s string, n int) []string {
	var chunks []string
	r := []rune(s)
	for len(r) > 0 {
		end := n
		if end > len(r) {
			end = len(r)
		}
		chunks = append(chunks, string(r[:end]))
		r = r[end:]
	}
	return chunks
}

// CheckUpdateURL validates a Swarm update URL before the bridge POSTs to it.
//
// When allowedHost (SWARM_ALLOWED_HOST, "host" or "host:port") is empty only
// the URL syntax is checked. Otherwise the URL must use https and point to
// that host (http is accepted only when the allowed host is localhost or
// 127.0.0.1). Without an explicit port in allowedHost, the URL must not name
// a non-default port either.
func CheckUpdateURL(raw, allowedHost string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("update_url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("update_url must use http or https")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("update_url has no host")
	}
	if u.User != nil {
		return fmt.Errorf("update_url must not contain credentials")
	}
	if allowedHost == "" {
		return nil
	}

	allowed, err := url.Parse("https://" + allowedHost)
	if err != nil {
		return fmt.Errorf("invalid SWARM_ALLOWED_HOST")
	}
	allowedName := strings.ToLower(allowed.Hostname())

	switch u.Scheme {
	case "https":
	case "http":
		if allowedName != "localhost" && allowedName != "127.0.0.1" {
			return fmt.Errorf("update_url must use https")
		}
	}

	if !strings.EqualFold(u.Hostname(), allowedName) {
		return fmt.Errorf("update_url host %q is not the allowed Swarm host", u.Hostname())
	}
	if allowed.Port() != "" {
		if u.Port() != allowed.Port() {
			return fmt.Errorf("update_url port must be %s", allowed.Port())
		}
	} else if p := u.Port(); p != "" && (u.Scheme != "https" || p != "443") && (u.Scheme != "http" || p != "80") {
		return fmt.Errorf("update_url port %s is not allowed", p)
	}
	return nil
}

// redactURL keeps only the scheme and host of a URL: Swarm update URLs carry
// a secret in their path
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<redacted>"
	}
	return u.Scheme + "://" + u.Host + "/<redacted>"
}

// redactErr removes the update URL (which contains a Swarm secret) from
// errors returned by net/http
func redactErr(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		urlErr.URL = redactURL(urlErr.URL)
	}
	return err
}
