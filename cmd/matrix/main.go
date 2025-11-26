package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mule-ai/mule/matrix-microservice/internal/config"
	"github.com/mule-ai/mule/matrix-microservice/internal/logger"
	"github.com/mule-ai/mule/matrix-microservice/internal/server"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logger
	appLogger, err := logger.New(&cfg.Logging)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	appLogger.Info("Starting Matrix microservice")

	// Create server
	srv, err := server.New(cfg, appLogger)
	if err != nil {
		appLogger.Error("Failed to create server: %v", err)
		log.Fatalf("Failed to create server: %v", err)
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			appLogger.Error("Failed to start server: %v", err)
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	appLogger.Info("Server started successfully on port %d", cfg.Server.Port)

	// Wait for shutdown signal
	<-sigChan
	appLogger.Info("Shutting down server...")

	// Stop server
	if err := srv.Stop(); err != nil {
		appLogger.Error("Error stopping server: %v", err)
	}

	appLogger.Info("Server stopped")
}