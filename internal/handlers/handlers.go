package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/models"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/services"
)

type Handler struct {
	cfg          *config.Config
	logger       zerolog.Logger
	hordeService *services.HordeService
	swarmService *services.SwarmService
	jobStorage   *services.JobStorage
}

// SetupRoutes configures all the routes for the application
func SetupRoutes(
	router *chi.Mux,
	cfg *config.Config,
	logger zerolog.Logger,
	hordeService *services.HordeService,
	swarmService *services.SwarmService,
	jobStorage *services.JobStorage,
) {
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

// handleSwarmTest handles incoming Swarm test webhook requests
func (h *Handler) handleSwarmTest(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug().Msg("Received request on /webhook/swarm-test endpoint")

	var req models.SwarmTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error().Err(err).Msg("failed to decode request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	h.logger.Debug().Msgf("Parsed request body: %v", req)

	// Validate request
	if req.Changelist == "" || req.UpdateURL == "" {
		h.logger.Error().Msg("missing required fields in request")
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	h.logger.Debug().Msgf("Request validated, proceeding to create a job in Horde for changelist: %s", req.Changelist)

	// Create Horde job
	jobID, err := h.hordeService.CreateJob(r.Context(), req.Changelist)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to create horde job")
		http.Error(w, "Failed to create job", http.StatusInternalServerError)
		return
	}

	h.logger.Info().Msgf("Created Horde job with ID: %s for change: %s", jobID, req.Changelist)

	// Store job mapping
	mapping := &models.JobMapping{
		SwarmTest:  req,
		HordeJobID: jobID,
		Status:     models.StatusPending,
		CreatedAt:  h.cfg.Clock.Now(),
		UpdatedAt:  h.cfg.Clock.Now(),
	}
	h.jobStorage.Store(jobID, mapping)

	// Update Swarm with initial status
	err = h.swarmService.UpdateStatus(r.Context(), req.UpdateURL, "running", []string{"Started Horde job " + h.cfg.Horde.Host + "/job/" + jobID}, jobID)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to update swarm status")
		// Don't return error as the job was created successfully
	}

	h.logger.Debug().Msgf("Initial Swarm status update sent for job ID: %s", jobID)

	// Returning status only as swarm does not care
	w.WriteHeader(http.StatusAccepted)
}

// handleListJobs returns a list of all current jobs
func (h *Handler) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs := h.jobStorage.List()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(jobs); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode jobs response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
