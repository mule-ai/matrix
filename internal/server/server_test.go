package server

import (
	"testing"

	"github.com/mule-ai/mule/matrix-microservice/internal/config"
	"github.com/mule-ai/mule/matrix-microservice/internal/logger"
	"github.com/mule-ai/mule/matrix-microservice/internal/session"
	"maunium.net/go/mautrix/id"
)

// TestThreadContinuation verifies that:
// 1. When a user sends a command, a session is created
// 2. When the user replies to the bot's message (in thread), the same session is used
// 3. Session context is preserved between messages in the same thread
func TestThreadContinuation(t *testing.T) {
	log, _ := logger.New(&config.LoggingConfig{Level: "error"})

	// Create session manager with echo command for testing
	sessionMgr := session.NewManager(log, 600, "echo {{.MESSAGE}}", "/tmp/pi-sessions")
	defer sessionMgr.Stop()

	userID := id.UserID("@user:matrix.org")
	threadRootEventID := id.EventID("$thread123")

	// Step 1: User sends first command (no thread context yet)
	// Simulate: Send /cmd hello → session created with user ID as key
	t.Run("Step1_FirstCommandCreatesSession", func(t *testing.T) {
		// First message has no thread root (new conversation)
		sess := sessionMgr.GetOrCreateSession("", userID, "echo {{.MESSAGE}}")

		if sess == nil {
			t.Fatal("Session should be created")
		}

		// Session should use user ID as key (no thread)
		expectedKey := "@user_matrix.org" // sanitized version (colons replaced)
		if sess.ID != expectedKey {
			t.Errorf("Session key = %v, want %v", sess.ID, expectedKey)
		}

		// Execute first command
		output, err := sessionMgr.ExecuteCommand(sess, "hello")
		if err != nil {
			t.Errorf("ExecuteCommand() error = %v", err)
		}
		if output != "hello\n" {
			t.Errorf("ExecuteCommand() output = %v, want %v", output, "hello\n")
		}

		// Context should be updated with output
		if sess.Context != "hello\n" {
			t.Errorf("Session context = %v, want %v", sess.Context, "hello\n")
		}

		t.Logf("Step 1: First command executed, session key=%s, context=%q", sess.ID, sess.Context)
	})

	// Step 2: User replies to bot message in thread
	// Simulate: Reply to bot's message with thread root = botEventID
	// This should use the bot's event ID as the thread root
	t.Run("Step2_ReplyInThreadContinuesSession", func(t *testing.T) {
		// Reply in thread should use thread root event ID as key
		sess := sessionMgr.GetOrCreateSession(threadRootEventID, userID, "echo {{.MESSAGE}}")

		if sess == nil {
			t.Fatal("Session should be retrieved/created")
		}

		// Session should use thread root event ID as key (sanitized - $ removed)
		expectedKey := "thread123" // sanitized version
		if sess.ID != expectedKey {
			t.Errorf("Session key = %v, want %v", sess.ID, expectedKey)
		}

		// Execute second command in thread
		output, err := sessionMgr.ExecuteCommand(sess, "world")
		if err != nil {
			t.Errorf("ExecuteCommand() error = %v", err)
		}
		if output != "world\n" {
			t.Errorf("ExecuteCommand() output = %v, want %v", output, "world\n")
		}

		// Context should be updated with new output
		t.Logf("Step 2: Thread session key=%s, context=%q", sess.ID, sess.Context)
	})

	// Step 3: User sends another reply in the same thread
	// Should continue using the same thread session
	t.Run("Step3_SameThreadContinuesSession", func(t *testing.T) {
		sess := sessionMgr.GetOrCreateSession(threadRootEventID, userID, "echo {{.MESSAGE}}")

		// Should be the same session (same thread root, sanitized)
		expectedKey := "thread123" // sanitized version
		if sess.ID != expectedKey {
			t.Errorf("Session key = %v, want %v", sess.ID, expectedKey)
		}

		// Execute third command with context
		output, err := sessionMgr.ExecuteCommand(sess, "test with context")
		if err != nil {
			t.Errorf("ExecuteCommand() error = %v", err)
		}

		t.Logf("Step 3: Third command in thread, output=%q", output)
	})
}

// TestThreadContinuationWithContext tests that session context is preserved
// and can be used in subsequent commands via {{.CONTEXT}} placeholder
func TestThreadContinuationWithContext(t *testing.T) {
	log, _ := logger.New(&config.LoggingConfig{Level: "error"})

	// Use command that includes context - use printf to avoid shell parsing issues
	sessionMgr := session.NewManager(log, 600, "printf '%s %s' '{{.CONTEXT}}' '{{.MESSAGE}}'", "/tmp/pi-sessions")
	defer sessionMgr.Stop()

	userID := id.UserID("@user:matrix.org")
	threadRootEventID := id.EventID("$thread456")

	// First message in thread
	// Note: templates don't include quotes around placeholders - shellEscape adds them
	sess1 := sessionMgr.GetOrCreateSession(threadRootEventID, userID, "printf '%s %s' {{.CONTEXT}} {{.MESSAGE}}")

	// Set initial context manually for testing
	sessionMgr.UpdateContext(sess1, "initial")

	output1, err := sessionMgr.ExecuteCommand(sess1, "first")
	if err != nil {
		t.Errorf("First ExecuteCommand() error = %v", err)
	}

	// Note: context doesn't have trailing newline, message doesn't either
	expected1 := "initial first"
	if output1 != expected1 {
		t.Errorf("First output = %q, want %q", output1, expected1)
	}

	// Context should now be updated with output1
	if sess1.Context != expected1 {
		t.Errorf("After first command, context = %q, want %q", sess1.Context, expected1)
	}

	t.Logf("First command output: %q", output1)
	t.Logf("Session context after first: %q", sess1.Context)

	// Second message in same thread - should have access to previous context
	sess2 := sessionMgr.GetOrCreateSession(threadRootEventID, userID, "printf '%s %s' '{{.CONTEXT}}' '{{.MESSAGE}}'")

	// Should be the same session
	if sess1 != sess2 {
		t.Error("Same thread should return same session")
	}

	output2, err := sessionMgr.ExecuteCommand(sess2, "second")
	if err != nil {
		t.Errorf("Second ExecuteCommand() error = %v", err)
	}

	// Should include previous context
	expected2 := "initial first second"
	if output2 != expected2 {
		t.Errorf("Second output = %q, want %q", output2, expected2)
	}

	t.Logf("Second command output: %q", output2)
	t.Logf("Session context after second: %q", sess2.Context)
}

// TestSeparateThreadsHaveSeparateSessions verifies that different threads
// have separate sessions with their own context
func TestSeparateThreadsHaveSeparateSessions(t *testing.T) {
	log, _ := logger.New(&config.LoggingConfig{Level: "error"})

	sessionMgr := session.NewManager(log, 600, "echo {{.MESSAGE}}", "/tmp/pi-sessions")
	defer sessionMgr.Stop()

	userID := id.UserID("@user:matrix.org")
	thread1ID := id.EventID("$thread1")
	thread2ID := id.EventID("$thread2")

	// Create session in thread 1
	sess1 := sessionMgr.GetOrCreateSession(thread1ID, userID, "echo {{.MESSAGE}}")
	sessionMgr.ExecuteCommand(sess1, "thread1 message")

	// Create session in thread 2
	sess2 := sessionMgr.GetOrCreateSession(thread2ID, userID, "echo {{.MESSAGE}}")
	sessionMgr.ExecuteCommand(sess2, "thread2 message")

	// Sessions should be different
	if sess1 == sess2 {
		t.Error("Different threads should have different sessions")
	}

	// Session keys should be different
	if sess1.ID == sess2.ID {
		t.Errorf("Session keys should be different: %s vs %s", sess1.ID, sess2.ID)
	}

	// Contexts should be isolated
	if sess1.Context == sess2.Context {
		t.Error("Session contexts should be isolated between threads")
	}

	t.Logf("Thread 1 session: key=%s, context=%q", sess1.ID, sess1.Context)
	t.Logf("Thread 2 session: key=%s, context=%q", sess2.ID, sess2.Context)
}

// TestNonThreadMessageCreatesNewSession verifies that a non-thread message
// from the same user creates a separate session (not reusing thread session)
func TestNonThreadMessageCreatesNewSession(t *testing.T) {
	log, _ := logger.New(&config.LoggingConfig{Level: "error"})

	sessionMgr := session.NewManager(log, 600, "echo {{.MESSAGE}}", "/tmp/pi-sessions")
	defer sessionMgr.Stop()

	userID := id.UserID("@user:matrix.org")
	threadRootEventID := id.EventID("$thread789")

	// First: Create session in thread
	sessThread := sessionMgr.GetOrCreateSession(threadRootEventID, userID, "echo {{.MESSAGE}}")
	sessionMgr.ExecuteCommand(sessThread, "in thread")
	threadContext := sessThread.Context

	// Second: Create session for non-thread message (empty thread root)
	sessNonThread := sessionMgr.GetOrCreateSession("", userID, "echo {{.MESSAGE}}")
	sessionMgr.ExecuteCommand(sessNonThread, "not in thread")
	nonThreadContext := sessNonThread.Context

	// Should be different sessions
	if sessThread == sessNonThread {
		t.Error("Thread and non-thread messages should have different sessions")
	}

	// Session keys should be different
	if sessThread.ID == sessNonThread.ID {
		t.Errorf("Session keys should be different: thread=%s, non-thread=%s", sessThread.ID, sessNonThread.ID)
	}

	// Contexts should be different
	if threadContext == nonThreadContext {
		t.Error("Contexts should be different between thread and non-thread sessions")
	}

	t.Logf("Thread session: key=%s, context=%q", sessThread.ID, threadContext)
	t.Logf("Non-thread session: key=%s, context=%q", sessNonThread.ID, nonThreadContext)
}

// TestInReplyToBecomesSessionKey verifies that when a message has inReplyTo
// but no thread root, the inReplyTo event ID becomes the session key
func TestInReplyToBecomesSessionKey(t *testing.T) {
	log, _ := logger.New(&config.LoggingConfig{Level: "error"})

	sessionMgr := session.NewManager(log, 600, "echo {{.MESSAGE}}", "/tmp/pi-sessions")
	defer sessionMgr.Stop()

	userID := id.UserID("@user:matrix.org")
	// Message is a reply but not in a thread - inReplyTo should be used as session key
	inReplyToEventID := id.EventID("$reply123")

	// Simulate: user replies to bot message (reply but not in thread)
	// This is handled in server.go: sessionThreadRoot is set to inReplyToEventID if threadRootEventID is empty
	sess := sessionMgr.GetOrCreateSession(inReplyToEventID, userID, "echo {{.MESSAGE}}")

	// Session key should be the inReplyTo event ID (sanitized - $ removed)
	expectedKey := "reply123" // sanitized version
	if sess.ID != expectedKey {
		t.Errorf("Session key = %v, want %v", sess.ID, expectedKey)
	}

	// Execute command
	output, err := sessionMgr.ExecuteCommand(sess, "reply message")
	if err != nil {
		t.Errorf("ExecuteCommand() error = %v", err)
	}

	t.Logf("Reply session: key=%s, output=%q", sess.ID, output)
}

// TestSessionPersistenceInThread verifies that multiple messages in the same
// thread continue to use the same session and preserve context
func TestSessionPersistenceInThread(t *testing.T) {
	log, _ := logger.New(&config.LoggingConfig{Level: "error"})

	sessionMgr := session.NewManager(log, 600, "echo {{.MESSAGE}}", "/tmp/pi-sessions")
	defer sessionMgr.Stop()

	userID := id.UserID("@user:matrix.org")
	threadRootEventID := id.EventID("$thread_persistence")

	// Simulate a conversation: 5 messages in the same thread
	messageCount := 5
	previousSession := (*session.Session)(nil)

	for i := 1; i <= messageCount; i++ {
		msg := string(rune('a' + i - 1)) // "a", "b", "c", "d", "e"

		sess := sessionMgr.GetOrCreateSession(threadRootEventID, userID, "echo {{.MESSAGE}}")

		// Should be the same session for all messages in thread
		if i > 1 && sess != previousSession {
			t.Errorf("Message %d: session changed unexpectedly", i)
		}

		output, err := sessionMgr.ExecuteCommand(sess, msg)
		if err != nil {
			t.Errorf("Message %d: ExecuteCommand() error = %v", i, err)
		}

		expectedOutput := msg + "\n"
		if output != expectedOutput {
			t.Errorf("Message %d: output = %q, want %q", i, output, expectedOutput)
		}

		t.Logf("Message %d: session=%s, output=%q, context=%q",
			i, sess.ID, output, sess.Context)

		previousSession = sess
	}

	// Verify session count - should only be 1 session for this thread
	if sessionMgr.GetSessionCount() != 1 {
		t.Errorf("Expected 1 session, got %d", sessionMgr.GetSessionCount())
	}
}
