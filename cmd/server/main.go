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
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	// Initialize logger
	log := logger.New()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// Create services and handler
	hordeService := services.NewHordeService(cfg, log)
	swarmService := services.NewSwarmService(cfg, log)
	jobStorage := services.NewJobStorage()

	// Setup routes
	handlers.SetupRoutes(router, cfg, log, hordeService, swarmService, jobStorage)

	// Start server
	go func() {
		log.Info().Msgf("starting server on port %d", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	// Initialize JobMonitor
	jobMonitor := monitor.New(cfg, log, jobStorage)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start JobMonitor in a goroutine
	go jobMonitor.Start(ctx)

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server")
	ctx, cancel = context.WithTimeout(context.Background(), time.Duration(cfg.Timeouts.Shutdown)*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal().Err(err).Msg("server forced to shutdown")
	}

	log.Info().Msg("server exited properly")
}
