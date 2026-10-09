package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/services"
)

// maxRequestBodySize limits the size of webhook request bodies
const maxRequestBodySize = 1 << 20

// WebhookTokenHeader and WebhookTokenParam carry the shared secret
// (WEBHOOK_TOKEN). Swarm test definitions can only customise the URL, so the
// query parameter is the usual way; the header is supported as well.
const (
	WebhookTokenHeader = "X-Webhook-Token"
	WebhookTokenParam  = "token"
)

type Handler struct {
	cfg          *config.Config
	logger       zerolog.Logger
	hordeService *services.HordeService
	swarmService *services.SwarmService
	jobStorage   *services.JobStorage

	// inflight tracks background job creations
	inflight sync.WaitGroup
}

// JobView is the public representation of a tracked job. It deliberately
// omits the Swarm update URL, which embeds a secret token.
type JobView struct {
	HordeJobID string           `json:"horde_job_id"`
	Changelist string           `json:"changelist"`
	Status     models.JobStatus `json:"status"`
	Reason     string           `json:"reason,omitempty"`
	JobURL     string           `json:"job_url"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// SetupRoutes configures all the routes for the application
func SetupRoutes(
	router *chi.Mux,
	cfg *config.Config,
	logger zerolog.Logger,
	hordeService *services.HordeService,
	swarmService *services.SwarmService,
	jobStorage *services.JobStorage,
) *Handler {
	h := &Handler{
		cfg:          cfg,
		logger:       logger,
		hordeService: hordeService,
		swarmService: swarmService,
		jobStorage:   jobStorage,
	}

	router.Get("/health", h.handleHealth)
	router.Post("/webhook/swarm-test", h.handleSwarmTest)
	router.Get("/jobs", h.handleListJobs)
	return h
}

// Wait blocks until background job creations are done or ctx expires
func (h *Handler) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleHealth handles health check requests
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "healthy"}); err != nil {
		h.logger.Error().Err(err).Msg("Failed to encode health check response")
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// authorized checks the optional shared secret sent by Swarm
func (h *Handler) authorized(r *http.Request) bool {
	token := h.cfg.Server.WebhookToken
	if token == "" {
		return true
	}
	got := r.Header.Get(WebhookTokenHeader)
	if got == "" {
		got = r.URL.Query().Get(WebhookTokenParam)
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// handleSwarmTest handles incoming Swarm test webhook requests.
//
// The request is validated and answered with 202 Accepted right away; the
// Horde job is created in the background (job creation with retries can take
// longer than the HTTP handler timeout) and the outcome is reported to Swarm
// through the update URL.
func (h *Handler) handleSwarmTest(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug().Msg("Received request on /webhook/swarm-test endpoint")

	if !h.authorized(r) {
		h.logger.Warn().Str("remote_addr", r.RemoteAddr).Msg("rejected webhook request with missing or invalid token")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.SwarmTestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodySize)).Decode(&req); err != nil {
		h.logger.Error().Err(err).Msg("failed to decode request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.Changelist = strings.TrimSpace(req.Changelist)
	req.UpdateURL = strings.TrimSpace(req.UpdateURL)

	// Validate request
	if req.Changelist == "" || req.UpdateURL == "" {
		h.logger.Error().Msg("missing required fields in request")
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// SSRF guard: only POST status updates to the configured Swarm host
	if err := services.CheckUpdateURL(req.UpdateURL, h.cfg.Swarm.AllowedHost); err != nil {
		h.logger.Warn().Err(err).Str("change", req.Changelist).Msg("rejected webhook request with a disallowed update_url")
		http.Error(w, "Invalid update_url: "+err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info().Str("change", req.Changelist).Msg("Swarm test request accepted, creating Horde job")

	h.inflight.Add(1)
	go func() {
		defer h.inflight.Done()
		h.createJob(req)
	}()

	w.WriteHeader(http.StatusAccepted)
}

// createJob creates the Horde job for a Swarm test request and reports the
// initial status to Swarm. It runs detached from the HTTP request.
func (h *Handler) createJob(req models.SwarmTestRequest) {
	log := h.logger.With().Str("change", req.Changelist).Logger()

	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.GetJobCreationBudget())
	defer cancel()

	jobID, err := h.hordeService.CreateJob(ctx, req.Changelist)
	if err != nil {
		log.Error().Err(err).Msg("failed to create horde job")
		// Fresh context: ctx may be exhausted by the creation attempts
		failCtx, failCancel := context.WithTimeout(context.Background(), h.cfg.GetSwarmTimeout())
		defer failCancel()
		if uerr := h.swarmService.UpdateStatus(failCtx, req.UpdateURL, services.SwarmStatusFail,
			services.SwarmMessages("Failed to create Horde job", err.Error()), ""); uerr != nil {
			log.Error().Err(uerr).Msg("failed to report job creation failure to swarm")
		}
		return
	}

	log = log.With().Str("job_id", jobID).Logger()
	log.Info().Msg("Created Horde job")

	// Report "running" before the job is stored: the monitor only sees the
	// job afterwards, so its updates can never be overtaken by this one.
	jobURL := h.hordeService.JobURL(jobID)
	runningReported := true
	if err := h.swarmService.UpdateStatus(ctx, req.UpdateURL, services.SwarmStatusRunning,
		services.SwarmMessages("Started Horde job", jobURL), jobURL); err != nil {
		// Not fatal: the monitor reports "running" once the job runs
		log.Error().Err(err).Msg("failed to update swarm status")
		runningReported = false
	}

	now := time.Now()
	if h.cfg.Clock != nil {
		now = h.cfg.Clock.Now()
	}
	mapping := &models.JobMapping{
		SwarmTest:  req,
		HordeJobID: jobID,
		Status:     models.StatusPending,
		// Swarm already knows the job is running (unless that update failed)
		RunningReported: runningReported,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := h.jobStorage.Store(jobID, mapping); err != nil {
		// The job is still tracked in memory, only a restart would lose it
		log.Error().Err(err).Msg("failed to persist job")
	}
}

// handleListJobs returns a list of all current jobs
func (h *Handler) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := h.jobStorage.List()
	views := make([]JobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, JobView{
			HordeJobID: job.HordeJobID,
			Changelist: job.SwarmTest.Changelist,
			Status:     job.Status,
			Reason:     job.Reason,
			JobURL:     h.hordeService.JobURL(job.HordeJobID),
			CreatedAt:  job.CreatedAt,
			UpdatedAt:  job.UpdatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(views); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode jobs response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
