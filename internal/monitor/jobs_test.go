package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/services"
)

// fakeClock is a settable clock
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// fakeSwarm records status updates and can be told to fail them
type fakeSwarm struct {
	mu      sync.Mutex
	fail    bool
	updates []models.SwarmUpdateRequest
}

func (f *fakeSwarm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		http.Error(w, "swarm down", http.StatusBadGateway)
		return
	}
	var u models.SwarmUpdateRequest
	_ = json.NewDecoder(r.Body).Decode(&u)
	f.updates = append(f.updates, u)
}

func (f *fakeSwarm) set(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *fakeSwarm) statuses() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s []string
	for _, u := range f.updates {
		s = append(s, u.Status)
	}
	return s
}

func (f *fakeSwarm) last() models.SwarmUpdateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updates[len(f.updates)-1]
}

// fakeHorde serves a settable job response and counts requests
type fakeHorde struct {
	mu       sync.Mutex
	response string
	status   int
	calls    atomic.Int32
}

func (f *fakeHorde) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		http.Error(w, "error", f.status)
		return
	}
	_, _ = w.Write([]byte(f.response))
}

func (f *fakeHorde) set(response string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.response = response
}

type testEnv struct {
	monitor   *JobMonitor
	storage   *services.JobStorage
	horde     *fakeHorde
	swarm     *fakeSwarm
	clock     *fakeClock
	updateURL string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{
		horde: &fakeHorde{},
		swarm: &fakeSwarm{},
		clock: &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
	}
	hordeServer := httptest.NewServer(env.horde)
	t.Cleanup(hordeServer.Close)
	swarmServer := httptest.NewServer(env.swarm)
	t.Cleanup(swarmServer.Close)
	env.updateURL = swarmServer.URL + "/update"

	cfg := &config.Config{
		Horde:   config.HordeConfig{Host: hordeServer.URL, APIKey: "k", Timeout: 5, TemplateId: "t", StreamId: "s"},
		Swarm:   config.SwarmConfig{Timeout: 5},
		Monitor: config.MonitorConfig{Interval: 1, MaxJobAge: 14400},
		Retry:   config.RetryConfig{MaxAttempts: 1},
		Clock:   env.clock,
	}
	storage, err := services.NewFileJobStorage(t.TempDir(), env.clock, zerolog.Nop())
	require.NoError(t, err)
	env.storage = storage

	logger := zerolog.Nop()
	env.monitor = New(cfg, logger, storage, services.NewHordeService(cfg, logger), services.NewSwarmService(cfg, logger))
	return env
}

func (e *testEnv) addJob(t *testing.T, id string, status models.JobStatus, createdAt time.Time) {
	t.Helper()
	require.NoError(t, e.storage.Store(id, &models.JobMapping{
		SwarmTest:  models.SwarmTestRequest{Changelist: "1", UpdateURL: e.updateURL},
		HordeJobID: id,
		Status:     status,
		CreatedAt:  createdAt,
	}))
}

func TestCheckJobsRetriesFinalReportUntilSwarmAccepts(t *testing.T) {
	env := newTestEnv(t)
	env.horde.set(`{"id":"job-1","state":"Running","batches":[{"error":"None","steps":[]}]}`)
	env.addJob(t, "job-1", models.StatusPending, env.clock.Now())

	// Pending -> running is reported once
	env.monitor.CheckJobs(context.Background())
	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, []string{"running"}, env.swarm.statuses())

	// Job fails while Swarm is unreachable: the job is kept for a retry
	env.horde.set(`{"id":"job-1","state":"Complete","batches":[{"error":"SyncingFailed","steps":[]}]}`)
	env.swarm.set(true)
	env.monitor.CheckJobs(context.Background())

	job, ok := env.storage.Get("job-1")
	require.True(t, ok, "job must be kept until Swarm got the final status")
	assert.Equal(t, models.StatusFailed, job.Status)
	assert.True(t, job.PendingReport)

	// Swarm is back: the final status is delivered without polling Horde again
	callsBefore := env.horde.calls.Load()
	env.swarm.set(false)
	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, callsBefore, env.horde.calls.Load(), "known final status must not be re-polled")
	assert.Equal(t, []string{"running", "fail"}, env.swarm.statuses())
	last := env.swarm.last()
	assert.Equal(t, "Horde job failed", last.Messages[0], "short summary first")
	assert.Equal(t, "Horde batch failed with error SyncingFailed", last.Messages[1])
	_, ok = env.storage.Get("job-1")
	assert.False(t, ok)
}

func TestCheckJobsWaitsForCompleteBeforeFailing(t *testing.T) {
	env := newTestEnv(t)
	env.addJob(t, "job-1", models.StatusRunning, env.clock.Now())

	// A step failed but the job is still running (the step may be retried)
	env.horde.set(`{"id":"job-1","state":"Running","batches":[{"error":"None","steps":[{"name":"Compile","outcome":"Failure"},{"state":"Running"}]}]}`)
	env.monitor.CheckJobs(context.Background())
	assert.Empty(t, env.swarm.statuses())
	_, ok := env.storage.Get("job-1")
	assert.True(t, ok)

	// The step was retried successfully and the job completed
	env.horde.set(`{"id":"job-1","state":"Complete","batches":[{"error":"None","steps":[{"name":"Compile","outcome":"Failure","retriedByUserInfo":{"id":"u","name":"Ann"}},{"name":"Compile","outcome":"Success"}]}]}`)
	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, []string{"pass"}, env.swarm.statuses())
}

func TestCheckJobsCancelledJobFailsImmediately(t *testing.T) {
	env := newTestEnv(t)
	env.addJob(t, "job-1", models.StatusRunning, env.clock.Now())
	env.horde.set(`{"id":"job-1","state":"Running","abortedByUserInfo":{"id":"u","name":"Bob"},"batches":[]}`)

	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, []string{"fail"}, env.swarm.statuses())
	assert.Contains(t, strings.Join(env.swarm.last().Messages, ""), "cancelled by Bob")
}

func TestCheckJobsMaxJobAge(t *testing.T) {
	env := newTestEnv(t)
	env.horde.set(`{"id":"old","state":"Running","batches":[]}`)
	env.addJob(t, "old", models.StatusRunning, env.clock.Now().Add(-5*time.Hour))
	env.addJob(t, "young", models.StatusRunning, env.clock.Now().Add(-time.Hour))

	// Swarm down: the timeout is recorded and kept for a retry
	env.swarm.set(true)
	env.monitor.CheckJobs(context.Background())
	job, ok := env.storage.Get("old")
	require.True(t, ok)
	assert.Equal(t, models.StatusFailed, job.Status)
	assert.True(t, job.PendingReport)
	assert.Equal(t, "Horde job did not finish within 4h0m0s", job.Reason)
	assert.Equal(t, int32(1), env.horde.calls.Load(), "only the young job is polled")

	env.swarm.set(false)
	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, []string{"fail"}, env.swarm.statuses())
	assert.Equal(t, []string{"Horde job failed", "Horde job did not finish within 4h0m0s"}, env.swarm.last().Messages)
	_, ok = env.storage.Get("old")
	assert.False(t, ok)
	_, ok = env.storage.Get("young")
	assert.True(t, ok)
}

func TestCheckJobsDeletedHordeJobIsReportedAsFailed(t *testing.T) {
	env := newTestEnv(t)
	env.horde.status = http.StatusNotFound
	env.addJob(t, "gone", models.StatusRunning, env.clock.Now())

	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, []string{"fail"}, env.swarm.statuses())
	_, ok := env.storage.Get("gone")
	assert.False(t, ok)
}

func TestCheckJobsUnknownStateKeepsJob(t *testing.T) {
	env := newTestEnv(t)
	env.horde.set(`{"id":"j","state":"SomethingNew","batches":[]}`)
	env.addJob(t, "j", models.StatusRunning, env.clock.Now())

	env.monitor.CheckJobs(context.Background())
	assert.Empty(t, env.swarm.statuses())
	_, ok := env.storage.Get("j")
	assert.True(t, ok)
}

func TestStartStopsOnContextCancel(t *testing.T) {
	env := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		env.monitor.Start(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitor did not stop after context cancellation")
	}
}

func TestCheckJobsReportsRunningOnlyOnce(t *testing.T) {
	env := newTestEnv(t)
	env.addJob(t, "job-1", models.StatusPending, env.clock.Now())

	// Setup batch runs, then the job waits for an agent, then runs again
	for _, state := range []string{"Running", "Waiting", "Running", "Waiting", "Running"} {
		env.horde.set(`{"id":"job-1","state":"` + state + `","batches":[{"error":"None","steps":[]}]}`)
		env.monitor.CheckJobs(context.Background())
	}
	assert.Equal(t, []string{"running"}, env.swarm.statuses())

	env.horde.set(`{"id":"job-1","state":"Complete","batches":[{"error":"None","steps":[{"outcome":"Success"}]}]}`)
	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, []string{"running", "pass"}, env.swarm.statuses())
}

func TestCheckJobsSkipsRunningWhenHandlerAlreadyReportedIt(t *testing.T) {
	env := newTestEnv(t)
	require.NoError(t, env.storage.Store("job-1", &models.JobMapping{
		SwarmTest:       models.SwarmTestRequest{Changelist: "1", UpdateURL: env.updateURL},
		HordeJobID:      "job-1",
		Status:          models.StatusPending,
		RunningReported: true,
		CreatedAt:       env.clock.Now(),
	}))
	env.horde.set(`{"id":"job-1","state":"Running","batches":[{"error":"None","steps":[]}]}`)
	env.monitor.CheckJobs(context.Background())
	assert.Empty(t, env.swarm.statuses())

	env.horde.set(`{"id":"job-1","state":"Complete","batches":[{"error":"None","steps":[{"outcome":"Success"}]}]}`)
	env.monitor.CheckJobs(context.Background())
	assert.Equal(t, []string{"pass"}, env.swarm.statuses())
}
