package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mule-ai/mule/matrix-microservice/internal/logger"
	"maunium.net/go/mautrix/id"
)

// shellEscape escapes a string for safe use in shell commands
// It wraps the string in single quotes and escapes any single quotes within
func shellEscape(s string) string {
	// Replace single quotes with '\'' (end quote, escaped single quote, start quote)
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

type Session struct {
	ID              string     // Session key (thread root event ID or user ID)
	UserID          id.UserID  // Owner of session
	ThreadRootEvent id.EventID // Thread root event ID (empty if no thread)
	LastActivity    time.Time  // Last message timestamp
	Context         string     // Previous command context/output
	Command         string     // Command template to use
	Mutex           sync.Mutex // Per-session lock
	SessionFile     string     // Path to session file for pi --session
}

type Manager struct {
	sessions        map[string]*Session
	mutex           sync.RWMutex
	logger          *logger.Logger
	sessionTimeout  time.Duration
	cleanupInterval time.Duration
	defaultCommand  string
	sessionDir      string // Directory for pi session files
	stopCleanup     chan struct{}
}

func NewManager(loggerInstance *logger.Logger, sessionTimeoutSeconds int, defaultCommand string, sessionDir string) *Manager {
	sessionTimeout := time.Duration(sessionTimeoutSeconds) * time.Second
	if sessionTimeout == 0 {
		sessionTimeout = 10 * time.Minute // Default 10 minutes
	}

	// Default session directory
	if sessionDir == "" {
		sessionDir = "/tmp/pi-sessions"
	}

	m := &Manager{
		sessions:        make(map[string]*Session),
		logger:          loggerInstance,
		sessionTimeout:  sessionTimeout,
		cleanupInterval: 60 * time.Second,
		defaultCommand:  defaultCommand,
		sessionDir:      sessionDir,
		stopCleanup:     make(chan struct{}),
	}

	// Ensure session directory exists
	if err := os.MkdirAll(m.sessionDir, 0755); err != nil {
		loggerInstance.Warn("Failed to create session directory %s: %v", m.sessionDir, err)
	}

	// Start background cleanup goroutine
	go m.cleanupLoop()

	m.logger.Info("Session manager initialized with timeout: %v, sessionDir: %s", sessionTimeout, sessionDir)

	return m
}

// Stop stops the session manager and cleanup goroutine
func (m *Manager) Stop() {
	m.logger.Info("Stopping session manager")
	close(m.stopCleanup)
}

// cleanupLoop runs cleanup periodically
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.Cleanup()
		case <-m.stopCleanup:
			m.logger.Info("Cleanup loop stopped")
			return
		}
	}
}

// Cleanup removes expired sessions
func (m *Manager) Cleanup() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	now := time.Now()
	expiredCount := 0

	for key, session := range m.sessions {
		if now.Sub(session.LastActivity) > m.sessionTimeout {
			delete(m.sessions, key)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		m.logger.Info("Cleaned up %d expired sessions (total active: %d)", expiredCount, len(m.sessions))
	}
}

// GetSessionKey generates a session key from thread root event ID or user ID
func (m *Manager) GetSessionKey(threadRootEventID id.EventID, userID id.UserID) string {
	if threadRootEventID != "" {
		// Sanitize event ID for use as filename - remove special chars like $ and -
		// that could cause issues in filenames or shell commands
		sanitized := strings.ReplaceAll(string(threadRootEventID), "$", "")
		sanitized = strings.ReplaceAll(sanitized, "-", "_")
		return sanitized
	}
	// Sanitize user ID for use as filename (replace : with _)
	return strings.ReplaceAll(string(userID), ":", "_")
}

// GetOrCreateSession retrieves or creates a session
func (m *Manager) GetOrCreateSession(threadRootEventID id.EventID, userID id.UserID, commandTemplate string) *Session {
	key := m.GetSessionKey(threadRootEventID, userID)

	m.mutex.Lock()
	defer m.mutex.Unlock()

	session, exists := m.sessions[key]
	if !exists {
		// Generate unique session file for this thread
		sessionFile := filepath.Join(m.sessionDir, fmt.Sprintf("thread_%s.jsonl", key))
		
		session = &Session{
			ID:              key,
			UserID:          userID,
			ThreadRootEvent: threadRootEventID,
			LastActivity:    time.Now(),
			Context:         "",
			Command:         commandTemplate,
			Mutex:           sync.Mutex{},
			SessionFile:     sessionFile,
		}
		m.sessions[key] = session
		m.logger.Info("Created new session: key=%s, userID=%s, threadRootEvent=%s, sessionFile=%s", key, userID, threadRootEventID, sessionFile)
	} else {
		// Update last activity
		session.LastActivity = time.Now()

		// Update command template if provided and different
		if commandTemplate != "" && session.Command == "" {
			session.Command = commandTemplate
		}

		m.logger.Debug("Retrieved existing session: key=%s, userID=%s", key, userID)
	}

	return session
}

// GetSession retrieves a session without creating one
func (m *Manager) GetSession(threadRootEventID id.EventID, userID id.UserID) *Session {
	key := m.GetSessionKey(threadRootEventID, userID)

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.sessions[key]
}

// GetSessionForUser finds any existing session for a user (returns most recent by LastActivity)
func (m *Manager) GetSessionForUser(userID id.UserID) *Session {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var recent *Session
	for _, session := range m.sessions {
		if session.UserID == userID {
			if recent == nil || session.LastActivity.After(recent.LastActivity) {
				recent = session
			}
		}
	}

	if recent != nil {
		m.logger.Debug("Found existing session for user %s: %s", userID, recent.ID)
	}

	return recent
}

// ExecuteCommand runs a shell command with the given message
func (m *Manager) ExecuteCommand(session *Session, message string) (string, error) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()

	// Update last activity
	session.LastActivity = time.Now()

	// Determine command to execute
	commandTemplate := session.Command
	if commandTemplate == "" {
		commandTemplate = m.defaultCommand
	}

	if commandTemplate == "" {
		return "", fmt.Errorf("no command template configured")
	}

	m.logger.Info("Executing command with template: %s", commandTemplate)
	m.logger.Debug("Message to execute: %s", message)

	// Build the full command by replacing placeholders:
	// {{.MESSAGE}} - the user's message (shell-escaped)
	// {{.CONTEXT}} - previous command output (shell-escaped)
	// {{.SESSION}} - path to the session file for pi --session
	fullCommand := strings.ReplaceAll(commandTemplate, "{{.MESSAGE}}", shellEscape(message))
	fullCommand = strings.ReplaceAll(fullCommand, "{{.CONTEXT}}", shellEscape(session.Context))
	fullCommand = strings.ReplaceAll(fullCommand, "{{.SESSION}}", shellEscape(session.SessionFile))

	m.logger.Info("Full command to execute: %s", fullCommand)

	// Execute the command with timeout
	cmd := exec.Command("sh", "-c", fullCommand)

	// Set a reasonable timeout (1 hour)
	timeout := 60 * time.Minute
	done := make(chan struct {
		output []byte
		err    error
	}, 1)

	go func() {
		output, err := cmd.CombinedOutput()
		done <- struct {
			output []byte
			err    error
		}{output, err}
	}()

	select {
	case result := <-done:
		outputStr := string(result.output)

		if result.err != nil {
			// Check if it's a timeout
			if strings.Contains(outputStr, "context deadline exceeded") || strings.Contains(result.err.Error(), "timeout") {
				m.logger.Error("Command timed out")
				return "", fmt.Errorf("command timed out after %v", timeout)
			}
			m.logger.Error("Command failed: %v, output: %s", result.err, outputStr)
			return "", fmt.Errorf("command failed: %v - %s", result.err, outputStr)
		}

		// Update session context with the output
		session.Context = outputStr
		m.logger.Info("Command executed successfully, output length: %d", len(outputStr))

		return outputStr, nil

	case <-time.After(timeout):
		// Kill the process if it times out
		if err := cmd.Process.Kill(); err != nil {
			m.logger.Error("Failed to kill timed out process: %v", err)
		}
		m.logger.Error("Command timed out after %v", timeout)
		return "", fmt.Errorf("command timed out after %v", timeout)
	}
}

// UpdateContext updates the session context
func (m *Manager) UpdateContext(session *Session, context string) {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	session.Context = context
	session.LastActivity = time.Now()
}

// GetContext retrieves the current session context
func (m *Manager) GetContext(session *Session) string {
	session.Mutex.Lock()
	defer session.Mutex.Unlock()
	return session.Context
}

// GetSessionCount returns the number of active sessions
func (m *Manager) GetSessionCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return len(m.sessions)
}
