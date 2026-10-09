package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/services"
)

type swarmCall struct {
	update      models.SwarmUpdateRequest
	jobInStore  bool
	requestPath string
}

type testEnv struct {
	router      *chi.Mux
	handler     *Handler
	storage     *services.JobStorage
	cfg         *config.Config
	hordeStatus int

	mu         sync.Mutex
	swarmCalls []swarmCall
	swarmURL   *url.URL
}

func newTestEnv(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	env := &testEnv{hordeStatus: http.StatusOK}

	horde := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if env.hordeStatus != http.StatusOK {
			http.Error(w, `{"message":"denied"}`, env.hordeStatus)
			return
		}
		_, _ = w.Write([]byte(`{"id":"job-1"}`))
	}))
	t.Cleanup(horde.Close)

	swarm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u models.SwarmUpdateRequest
		_ = json.NewDecoder(r.Body).Decode(&u)
		_, inStore := env.storage.Get("job-1")
		env.mu.Lock()
		env.swarmCalls = append(env.swarmCalls, swarmCall{update: u, jobInStore: inStore, requestPath: r.URL.Path})
		env.mu.Unlock()
	}))
	t.Cleanup(swarm.Close)
	env.swarmURL, _ = url.Parse(swarm.URL)

	env.cfg = &config.Config{
		Horde: config.HordeConfig{Host: horde.URL, PublicURL: "https://horde.example.com", APIKey: "k",
			Timeout: 5, TemplateId: "t", StreamId: "s"},
		Swarm:   config.SwarmConfig{Timeout: 5},
		Monitor: config.MonitorConfig{Interval: 30, MaxJobAge: 14400},
		Retry:   config.RetryConfig{MaxAttempts: 1},
		Clock:   config.RealClock{},
	}
	if mutate != nil {
		mutate(env.cfg)
	}

	logger := zerolog.Nop()
	env.storage = services.NewJobStorage(nil)
	env.router = chi.NewRouter()
	env.handler = SetupRoutes(env.router, env.cfg, logger,
		services.NewHordeService(env.cfg, logger), services.NewSwarmService(env.cfg, logger), env.storage)
	return env
}

func (e *testEnv) updateURL() string {
	return e.swarmURL.String() + "/api/v10/testruns/1/secret-token"
}

func (e *testEnv) calls() []swarmCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]swarmCall(nil), e.swarmCalls...)
}

func (e *testEnv) post(t *testing.T, ctx context.Context, target string, body any, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(data)).WithContext(ctx)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) wait(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, e.handler.Wait(ctx))
}

func TestWebhookAcceptsAndCreatesJobInBackground(t *testing.T) {
	env := newTestEnv(t, nil)

	rec := env.post(t, context.Background(), "/webhook/swarm-test",
		models.SwarmTestRequest{Changelist: "123", UpdateURL: env.updateURL()}, nil)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	env.wait(t)

	calls := env.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "running", calls[0].update.Status)
	assert.Equal(t, "https://horde.example.com/job/job-1", calls[0].update.JobUrl)
	assert.False(t, calls[0].jobInStore, "running must be reported before the job is stored")

	job, ok := env.storage.Get("job-1")
	require.True(t, ok)
	assert.Equal(t, models.StatusPending, job.Status)
	assert.False(t, job.PendingReport)
	assert.Equal(t, "123", job.SwarmTest.Changelist)
}

func TestWebhookReportsCreationFailureWithDetachedContext(t *testing.T) {
	env := newTestEnv(t, nil)
	env.hordeStatus = http.StatusForbidden

	// The request context is cancelled as soon as the handler returned
	ctx, cancel := context.WithCancel(context.Background())
	rec := env.post(t, ctx, "/webhook/swarm-test",
		models.SwarmTestRequest{Changelist: "123", UpdateURL: env.updateURL()}, nil)
	cancel()
	assert.Equal(t, http.StatusAccepted, rec.Code)
	env.wait(t)

	calls := env.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "fail", calls[0].update.Status)
	assert.Equal(t, "Failed to create Horde job", calls[0].update.Messages[0])
	for _, m := range calls[0].update.Messages {
		assert.LessOrEqual(t, len([]rune(m)), services.SwarmMaxMessageLength)
	}
	assert.Contains(t, strings.Join(calls[0].update.Messages, ""), "403")
	assert.Empty(t, env.storage.List())
}

func TestWebhookValidation(t *testing.T) {
	env := newTestEnv(t, nil)

	rec := env.post(t, context.Background(), "/webhook/swarm-test", models.SwarmTestRequest{Changelist: "1"}, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	req := httptest.NewRequest(http.MethodPost, "/webhook/swarm-test", strings.NewReader("{not json"))
	rec = httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = env.post(t, context.Background(), "/webhook/swarm-test",
		models.SwarmTestRequest{Changelist: "1", UpdateURL: "file:///etc/passwd"}, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	env.wait(t)
	assert.Empty(t, env.calls())
}

func TestWebhookToken(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) { c.Server.WebhookToken = "s3cret" })
	body := models.SwarmTestRequest{Changelist: "1", UpdateURL: env.updateURL()}

	tests := []struct {
		name   string
		target string
		header map[string]string
		want   int
	}{
		{"missing token", "/webhook/swarm-test", nil, http.StatusUnauthorized},
		{"wrong header", "/webhook/swarm-test", map[string]string{WebhookTokenHeader: "nope"}, http.StatusUnauthorized},
		{"wrong query param", "/webhook/swarm-test?token=nope", nil, http.StatusUnauthorized},
		{"valid header", "/webhook/swarm-test", map[string]string{WebhookTokenHeader: "s3cret"}, http.StatusAccepted},
		{"valid query param", "/webhook/swarm-test?token=s3cret", nil, http.StatusAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.post(t, context.Background(), tt.target, body, tt.header)
			assert.Equal(t, tt.want, rec.Code)
		})
	}
	env.wait(t)
}

func TestWebhookNoTokenConfigured(t *testing.T) {
	env := newTestEnv(t, nil)
	rec := env.post(t, context.Background(), "/webhook/swarm-test",
		models.SwarmTestRequest{Changelist: "1", UpdateURL: env.updateURL()}, nil)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	env.wait(t)
}

func TestWebhookSwarmAllowedHost(t *testing.T) {
	env := newTestEnv(t, nil)
	// The fake Swarm listens on http://127.0.0.1:<port>: http is allowed for 127.0.0.1
	env.cfg.Swarm.AllowedHost = env.swarmURL.Host

	rec := env.post(t, context.Background(), "/webhook/swarm-test",
		models.SwarmTestRequest{Changelist: "1", UpdateURL: env.updateURL()}, nil)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	for _, bad := range []string{
		"https://evil.example.com/api/v10/testruns/1/x",
		"http://169.254.169.254/latest/meta-data",
		"https://127.0.0.1:1/other-port",
	} {
		rec = env.post(t, context.Background(), "/webhook/swarm-test",
			models.SwarmTestRequest{Changelist: "1", UpdateURL: bad}, nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code, bad)
	}

	env.wait(t)
	assert.Len(t, env.calls(), 1, "only the allowed update URL is called")
}

func TestListJobsHidesUpdateURL(t *testing.T) {
	env := newTestEnv(t, nil)
	require.NoError(t, env.storage.Store("job-9", &models.JobMapping{
		SwarmTest:  models.SwarmTestRequest{Changelist: "42", UpdateURL: "https://swarm/api/v10/testruns/1/secret-token"},
		HordeJobID: "job-9",
		Status:     models.StatusRunning,
	}))

	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/jobs", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret-token")
	assert.NotContains(t, rec.Body.String(), "update_url")

	var jobs []JobView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &jobs))
	require.Len(t, jobs, 1)
	assert.Equal(t, "42", jobs[0].Changelist)
	assert.Equal(t, "https://horde.example.com/job/job-9", jobs[0].JobURL)
}

func TestRequestLoggerOmitsQueryString(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	h := RequestLogger(logger, "/health")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhook/swarm-test?token=s3cret", nil))
	assert.Contains(t, buf.String(), `"path":"/webhook/swarm-test"`)
	assert.Contains(t, buf.String(), `"status":202`)
	assert.NotContains(t, buf.String(), "s3cret")

	buf.Reset()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Empty(t, buf.String(), "health checks are not logged")
}
