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

	// Generate pickle key if not set
	if cfg.EnsurePickleKey() {
		log.Println("Generated new pickle key, saving to config...")
		if err := config.SaveConfig(cfg); err != nil {
			log.Printf("Warning: Failed to save pickle key to config: %v", err)
		} else {
			log.Println("Pickle key saved to config.yaml")
		}
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

	// After Matrix client is initialized, save any updated credentials
	// (e.g., device ID may have changed after first login)
	if err := config.SaveConfig(cfg); err != nil {
		appLogger.Warn("Failed to save config after startup: %v", err)
	} else {
		appLogger.Debug("Config saved successfully")
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
