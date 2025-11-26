package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mule-ai/mule/matrix-microservice/internal/config"
	"github.com/mule-ai/mule/matrix-microservice/internal/logger"
	"github.com/mule-ai/mule/matrix-microservice/internal/matrix"
	"github.com/mule-ai/mule/matrix-microservice/internal/webhook"
	"maunium.net/go/mautrix/id"
)

type Server struct {
	config     *config.Config
	router     *chi.Mux
	matrix     *matrix.Client
	httpServer *http.Server
	logger     *logger.Logger
	webhook    *webhook.Dispatcher
}

// Implement the matrix.MessageHandler interface
func (s *Server) HandleMessage(roomID id.RoomID, sender id.UserID, message string) {
	s.logger.Info("Processing Matrix message from %s: %s", sender, message)

	// Extract command from message
	command := s.webhook.ExtractCommand(message)

	// Dispatch to webhook
	reply, err := s.webhook.Dispatch(message, command)
	if err != nil {
		s.logger.Error("Failed to dispatch webhook: %v", err)
		return
	}

	// Send reply back to Matrix if not empty
	if reply != "" {
		s.logger.Info("Sending webhook reply to Matrix: %s", reply)
		if err := s.matrix.SendMessage(reply); err != nil {
			s.logger.Error("Failed to send reply to Matrix: %v", err)
		}
	} else {
		s.logger.Debug("No reply to send to Matrix")
	}
}

func New(cfg *config.Config, logger *logger.Logger) (*Server, error) {
	// Initialize Matrix client
	matrixClient, err := matrix.New(&cfg.Matrix, logger)
	if err != nil {
		logger.Error("Failed to initialize Matrix client: %v", err)
		return nil, fmt.Errorf("failed to initialize Matrix client: %w", err)
	}

	// Initialize webhook dispatcher
	webhookDispatcher := webhook.New(&cfg.Webhook, logger)

	// Create router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	s := &Server{
		config:  cfg,
		router:  r,
		matrix:  matrixClient,
		logger:  logger,
		webhook: webhookDispatcher,
	}

	// Set the server as the message handler for the Matrix client
	matrixClient.SetMessageHandler(s)

	s.routes()

	return s, nil
}

func (s *Server) routes() {
	s.router.Get("/health", s.handleHealth)
	s.router.Get("/status", s.handleStatus)
	s.router.Post("/message", s.handleMessage)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Health check endpoint called")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Status endpoint called")
	status := map[string]interface{}{
		"status": "running",
		"matrix": map[string]string{
			"room_id": s.config.Matrix.RoomID,
			"user_id": s.config.Matrix.UserID,
		},
		"webhooks": map[string]interface{}{
			"default":            s.config.Webhook.Default,
			"commands":           s.config.Webhook.Commands,
			"template":           s.config.Webhook.Template,
			"command_templates":  s.config.Webhook.CommandTemplates,
			"jq_selector":        s.config.Webhook.JQSelector,
			"command_selectors":  s.config.Webhook.CommandSelectors,
			"skip_empty":         s.config.Webhook.SkipEmpty,
			"timeout":            s.config.Webhook.Timeout,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

type MessageRequest struct {
	Message string `json:"message"`
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("Message endpoint called")

	var req MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error("Invalid JSON in request: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	s.logger.Info("Received message: %s", req.Message)

	// Send message to Matrix
	if err := s.matrix.SendMessage(req.Message); err != nil {
		s.logger.Error("Failed to send message to Matrix: %v", err)
		http.Error(w, "Failed to send message to Matrix", http.StatusInternalServerError)
		return
	}

	s.logger.Info("Message sent to Matrix successfully")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Server.Port)
	s.logger.Info("Starting server on %s", addr)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	return s.httpServer.ListenAndServe()
}

func (s *Server) Stop() error {
	s.logger.Info("Stopping server")
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}