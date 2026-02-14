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
	"github.com/mule-ai/mule/matrix-microservice/internal/session"
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
	sessionMgr *session.Manager
}

// Implement the matrix.MessageHandler interface
func (s *Server) HandleMessage(roomID id.RoomID, sender id.UserID, message string, inReplyToEventID id.EventID, threadRootEventID id.EventID, eventID id.EventID) {
	s.logger.Info("Processing Matrix message from %s: %s (inReplyTo: %s, threadRoot: %s, eventID: %s)", sender, message, inReplyToEventID, threadRootEventID, eventID)

	// Check if command execution is enabled
	if s.config.Webhook.EnableCommands && s.webhook.HasCommandPrefix(message) {
		// Command execution mode
		s.handleCommandExecution(sender, message, inReplyToEventID, threadRootEventID, eventID)
		return
	}

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
		// Only use threadRootEventID for reply - do NOT fall back to inReplyToEventID
		// as that can persist from previous messages and cause replies to go to wrong thread
		replyEventID := threadRootEventID

		// Send with reply and mention if we have thread info
		if replyEventID != "" {
			if err := s.matrix.SendMessage(reply, matrix.WithReplyTo(replyEventID), matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send reply to Matrix: %v", err)
			}
		} else {
			// For non-reply messages, still mention the sender
			if err := s.matrix.SendMessage(reply, matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send reply to Matrix: %v", err)
			}
		}
	} else {
		s.logger.Debug("No reply to send to Matrix")
	}
}

// handleCommandExecution processes command messages and executes them
func (s *Server) handleCommandExecution(sender id.UserID, message string, inReplyToEventID id.EventID, threadRootEventID id.EventID, eventID id.EventID) {
	s.logger.Info("Handling command execution for message from %s", sender)

	// Extract command name and arguments from the message
	cmdName, args := s.webhook.GetCommandFromPrefix(message)
	s.logger.Info("Extracted command: %s, args: %s", cmdName, args)

	// Determine the session key
	// If this is a reply (inReplyToEventID is set), find any existing session for this user
	// This allows continuing a conversation when replying to the bot's message
	var sessionThreadRoot id.EventID
	
	if inReplyToEventID != "" && len(inReplyToEventID) > 0 && string(inReplyToEventID)[0] == '$' {
		// This is a reply - find any existing session for this user
		existingSession := s.sessionMgr.GetSessionForUser(sender)
		if existingSession != nil {
			s.logger.Info("Found existing session for reply, continuing session: %s", existingSession.ID)
			sessionThreadRoot = id.EventID(existingSession.ID)
		}
	}

	// If no existing session found (or not a reply), create new session with event ID
	if sessionThreadRoot == "" {
		sessionThreadRoot = eventID
	}

	// Determine reply event ID for sending the response
	replyEventID := threadRootEventID
	if replyEventID == "" {
		replyEventID = inReplyToEventID
	}

	// Get command template - first try command-specific template, then default
	commandTemplate := ""
	if cmdName != "" {
		if tpl, exists := s.config.Webhook.CommandTemplates[cmdName]; exists {
			commandTemplate = tpl
			s.logger.Info("Using command-specific template for: %s", cmdName)
		}
	}
	if commandTemplate == "" {
		commandTemplate = s.config.Webhook.DefaultCommand
		s.logger.Info("Using default command template: %s", commandTemplate)
	}

	// If no command template configured, return error
	if commandTemplate == "" {
		errorMsg := "No command template configured. Please set default_command or command_templates in config."
		s.logger.Error(errorMsg)
		if replyEventID != "" {
			if err := s.matrix.SendMessage(errorMsg, matrix.WithReplyTo(replyEventID), matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send error message: %v", err)
			}
		} else {
			if err := s.matrix.SendMessage(errorMsg, matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send error message: %v", err)
			}
		}
		return
	}

	// Get or create session - passing empty threadRootEventID will cause the session manager
	// to use userID as the session key, ensuring all messages from same user share context
	session := s.sessionMgr.GetOrCreateSession(sessionThreadRoot, sender, commandTemplate)
	s.logger.Debug("Session retrieved/created: key=%s, userID=%s, command=%s", session.ID, session.UserID, session.Command)

	// Execute the command
	reply, err := s.sessionMgr.ExecuteCommand(session, args)
	if err != nil {
		errorMsg := fmt.Sprintf("Command execution failed: %v", err)
		s.logger.Error(errorMsg)
		if replyEventID != "" {
			if err := s.matrix.SendMessage(errorMsg, matrix.WithReplyTo(replyEventID), matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send error message: %v", err)
			}
		} else {
			if err := s.matrix.SendMessage(errorMsg, matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send error message: %v", err)
			}
		}
		return
	}

	// Send the reply
	if reply != "" {
		s.logger.Info("Sending command output to Matrix (length: %d)", len(reply))
		if replyEventID != "" {
			if err := s.matrix.SendMessage(reply, matrix.WithReplyTo(replyEventID), matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send command output: %v", err)
			}
		} else {
			if err := s.matrix.SendMessage(reply, matrix.WithMention(sender)); err != nil {
				s.logger.Error("Failed to send command output: %v", err)
			}
		}
	} else {
		s.logger.Info("Command executed successfully but produced no output")
	}
}

func New(cfg *config.Config, loggerInstance *logger.Logger) (*Server, error) {
	// Initialize Matrix client
	matrixClient, err := matrix.New(&cfg.Matrix, loggerInstance)
	if err != nil {
		loggerInstance.Error("Failed to initialize Matrix client: %v", err)
		return nil, fmt.Errorf("failed to initialize Matrix client: %w", err)
	}

	// Update config with current device ID (may have changed after first login)
	currentDeviceID := matrixClient.GetDeviceID()
	if currentDeviceID != cfg.Matrix.DeviceID && currentDeviceID != "" {
		loggerInstance.Info("Device ID changed from %s to %s, updating config", cfg.Matrix.DeviceID, currentDeviceID)
		cfg.Matrix.DeviceID = currentDeviceID
	}

	// Initialize webhook dispatcher
	webhookDispatcher := webhook.New(&cfg.Webhook, loggerInstance)

	// Initialize session manager
	sessionMgr := session.NewManager(loggerInstance, cfg.Webhook.SessionTimeout, cfg.Webhook.DefaultCommand, "/tmp/pi-sessions")

	// Create router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	s := &Server{
		config:     cfg,
		router:     r,
		matrix:     matrixClient,
		logger:     loggerInstance,
		webhook:    webhookDispatcher,
		sessionMgr: sessionMgr,
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
			"default":           s.config.Webhook.Default,
			"commands":          s.config.Webhook.Commands,
			"template":          s.config.Webhook.Template,
			"command_templates": s.config.Webhook.CommandTemplates,
			"jq_selector":       s.config.Webhook.JQSelector,
			"command_selectors": s.config.Webhook.CommandSelectors,
			"skip_empty":        s.config.Webhook.SkipEmpty,
			"timeout":           s.config.Webhook.Timeout,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

type MessageRequest struct {
	Message  string `json:"message"`
	AsFile   bool   `json:"as_file,omitempty"`
	Filename string `json:"filename,omitempty"`
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("Message endpoint called")

	var req MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error("Invalid JSON in request: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Set default values for optional parameters
	if req.Filename == "" {
		req.Filename = "message.md"
	}

	s.logger.Info("Received message: %s, as_file: %t, filename: %s", req.Message, req.AsFile, req.Filename)

	// Send message to Matrix
	var err error
	if req.AsFile {
		err = s.matrix.SendFile(req.Message, req.Filename)
	} else {
		err = s.matrix.SendMessage(req.Message)
	}

	if err != nil {
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
	if s.sessionMgr != nil {
		s.sessionMgr.Stop()
	}
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}
