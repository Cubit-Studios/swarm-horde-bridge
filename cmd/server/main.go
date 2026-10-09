package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cubit-Studios/swarm-horde-bridge/internal/config"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/handlers"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/monitor"
	"github.com/Cubit-Studios/swarm-horde-bridge/internal/services"
	"github.com/Cubit-Studios/swarm-horde-bridge/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	// Parse command line flags
	defaultConfig := os.Getenv("CONFIG_FILE")
	if defaultConfig == "" {
		defaultConfig = "config.yaml"
	}
	configPath := flag.String("config", defaultConfig, "path to the optional config file (env: CONFIG_FILE)")
	healthcheck := flag.Bool("healthcheck", false, "query the local /health endpoint and exit 0 if healthy (for container health checks)")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	// Initialize logger
	log := logger.New()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}
	if cfg.ConfigFile != "" {
		log.Info().Str("config_file", cfg.ConfigFile).Msg("loaded configuration file")
	} else {
		log.Info().Str("config_file", *configPath).Msg("config file not found, using environment variables only")
	}

	// Create services and job storage
	jobStorage, err := services.NewFileJobStorage(cfg.DataDir, cfg.Clock, log)
	if err != nil {
		log.Fatal().Err(err).Str("data_dir", cfg.DataDir).Msg("failed to open job store")
	}
	log.Info().Str("path", jobStorage.Path()).Int("jobs", len(jobStorage.List())).Msg("job store loaded")

	if cfg.Server.WebhookToken == "" {
		log.Warn().Msg("WEBHOOK_TOKEN is not set: the webhook endpoint is unauthenticated")
	}
	if cfg.Swarm.AllowedHost == "" {
		log.Warn().Msg("SWARM_ALLOWED_HOST is not set: status updates are sent to any update_url received")
	}

	hordeService := services.NewHordeService(cfg, log)
	swarmService := services.NewSwarmService(cfg, log)

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(handlers.RequestLogger(log, "/health"))
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Must exceed the handler timeout: job creation calls Horde synchronously
		WriteTimeout: 65 * time.Second,
	}

	// Setup routes
	handler := handlers.SetupRoutes(router, cfg, log, hordeService, swarmService, jobStorage)

	// Start server
	go func() {
		log.Info().Msgf("starting server on port %d", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	// Start JobMonitor in a goroutine
	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	jobMonitor := monitor.New(cfg, log, jobStorage, hordeService, swarmService)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		jobMonitor.Start(monitorCtx)
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server")
	stopMonitor()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.GetShutdownTimeout())
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server forced to shutdown")
	}

	// Wait for the monitor to finish its current pass, and for job creations
	// started by webhooks, so job store writes are not cut off
	select {
	case <-monitorDone:
	case <-ctx.Done():
		log.Warn().Msg("job monitor did not stop before the shutdown timeout")
	}
	if err := handler.Wait(ctx); err != nil {
		log.Warn().Msg("pending Horde job creations did not finish before the shutdown timeout")
	}

	log.Info().Msg("server exited properly")
}

// runHealthcheck queries the local health endpoint (port from PORT, default 8080)
// and returns the process exit code. It needs no shell or curl, so it works in
// distroless images.
func runHealthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck failed:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck failed: status", resp.StatusCode)
		return 1
	}
	return 0
}
