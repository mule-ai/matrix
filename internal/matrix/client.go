package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"github.com/mule-ai/mule/matrix-microservice/internal/config"
	"github.com/mule-ai/mule/matrix-microservice/internal/logger"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	_ "github.com/mattn/go-sqlite3"
)

// MessageHandler defines the interface for handling incoming Matrix messages
type MessageHandler interface {
	HandleMessage(roomID id.RoomID, sender id.UserID, message string)
}

type sessionRequestInfo struct {
	lastRequested time.Time
	retryCount    int
}

type Client struct {
	client         *mautrix.Client
	roomID         string
	deviceID       string
	logger         *logger.Logger
	cryptoHelper   *cryptohelper.CryptoHelper
	config         *config.MatrixConfig
	messageHandler MessageHandler
	mentionRegex   *regexp.Regexp
	slashCommandRegex *regexp.Regexp
	requestedSessionMutex sync.Mutex
	requestedSessions     map[string]*sessionRequestInfo
}

func New(cfg *config.MatrixConfig, logger *logger.Logger) (*Client, error) {
	logger.Info("Initializing Matrix client for user %s", cfg.UserID)

	client, err := mautrix.NewClient(cfg.Homeserver, id.UserID(cfg.UserID), cfg.AccessToken)
	if err != nil {
		logger.Error("Failed to create Matrix client: %v", err)
		return nil, fmt.Errorf("failed to create Matrix client: %w", err)
	}

	client.DeviceID = id.DeviceID(cfg.DeviceID)

	c := &Client{
		client:   client,
		roomID:   cfg.RoomID,
		deviceID: cfg.DeviceID,
		logger:   logger,
		config:   cfg,
		requestedSessions: make(map[string]*sessionRequestInfo),
	}

	c.mentionRegex = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)
	c.slashCommandRegex = regexp.MustCompile(`\/([a-zA-Z0-9_]+)`)

	// Setup syncer
	syncer := mautrix.NewDefaultSyncer()
	client.Syncer = syncer

	// Register event handler
	syncer.OnEvent(c.processEvent)

	// Setup crypto helper
	if cfg.EnableEncryption {
		logger.Info("Encryption enabled, setting up crypto helper")
		if err := c.setupEncryption(); err != nil {
			logger.Error("Failed to setup encryption: %v", err)
			logger.Warn("Continuing without encryption")
		} else {
			logger.Info("Encryption setup complete")
		}
	}

	// Start syncing in background
	go func() {
		c.logger.Info("Starting Matrix sync loop...")
		if err := client.Sync(); err != nil {
			c.logger.Error("Sync loop failed: %v", err)
			c.logger.Info("Sync error is not fatal - bot will continue running but may not receive new messages")
		}
	}()

	logger.Info("Matrix client initialized successfully")

	return c, nil
}

func (c *Client) SetMessageHandler(handler MessageHandler) {
	c.messageHandler = handler
}

func (c *Client) setupEncryption() error {
	// Setup crypto helper
	cryptoHelper, err := c.setupCryptoHelper()
	if err != nil {
		return fmt.Errorf("failed to setup crypto helper: %w", err)
	}

	c.client.Crypto = cryptoHelper
	c.cryptoHelper = cryptoHelper

	// Wait for initial sync
	readyChan := make(chan bool)
	var once sync.Once
	syncer := c.client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnSync(func(ctx context.Context, resp *mautrix.RespSync, since string) bool {
		// Process to-device events first (they may contain room keys needed for decryption)
		if len(resp.ToDevice.Events) > 0 {
			c.logger.Info("Processing to-device events", "count", len(resp.ToDevice.Events))
			for i, evt := range resp.ToDevice.Events {
				c.logger.Debug("Processing to-device event",
					"index", i,
					"type", evt.Type,
					"sender", evt.Sender)

				// Handle room key events specially to improve logging
				switch evt.Type {
				case event.ToDeviceRoomKey:
					if key, ok := evt.Content.Parsed.(*event.RoomKeyEventContent); ok {
						c.logger.Info("Received room key",
							"algorithm", key.Algorithm,
							"room_id", key.RoomID,
							"session_id", key.SessionID)
					}
				case event.ToDeviceForwardedRoomKey:
					if key, ok := evt.Content.Parsed.(*event.ForwardedRoomKeyEventContent); ok {
						c.logger.Info("Received forwarded room key",
							"algorithm", key.Algorithm,
							"room_id", key.RoomID,
							"session_id", key.SessionID,
							"sender_key", key.SenderKey)
					}
				case event.ToDeviceRoomKeyRequest:
					if req, ok := evt.Content.Parsed.(*event.RoomKeyRequestEventContent); ok {
						c.logger.Debug("Received room key request",
							"request_id", req.RequestID,
							"action", req.Action)
					}
				}
				// The crypto helper will automatically process these to-device events
				// when ProcessSyncResponse is called by the syncer
			}
		}

		once.Do(func() {
			close(readyChan)
		})
		return true
	})

	if c.config.SkipInitialSync {
		c.logger.Info("Skipping initial sync wait (skip_initial_sync=true)")
		c.logger.Info("Bot will start immediately but may take time to process existing messages")
	} else {
		syncTimeout := time.Duration(c.config.SyncTimeout) * time.Second
		c.logger.Info("Waiting for initial sync to complete (timeout: %v)...", syncTimeout)
		select {
		case <-readyChan:
			c.logger.Info("Initial sync complete")
		case <-time.After(syncTimeout):
			c.logger.Warn("Initial sync timeout after %v, but continuing...", syncTimeout)
			c.logger.Info("This is normal for accounts with many rooms or messages")
		}
	}

	// Share keys with the server
	c.logger.Info("Uploading device keys...")
	ctx := context.Background()
	machine := cryptoHelper.Machine()
	if err := machine.ShareKeys(ctx, 0); err != nil {
		c.logger.Error("Failed to share keys: %v", err)
	}

	// Verify with recovery key
	c.logger.Info("Attempting to verify with recovery key...")
	if err := c.verifyWithRecoveryKey(machine); err != nil {
		c.logger.Error("Failed with initial verification: %v", err)
		time.Sleep(5 * time.Second)
		
		c.logger.Info("Retrying key verification...")
		if err := c.verifyWithRecoveryKey(machine); err != nil {
			return fmt.Errorf("failed to verify with recovery key after retry: %w", err)
		}
	}
	c.logger.Info("Key verification successful")

	return nil
}

func (c *Client) setupCryptoHelper() (*cryptohelper.CryptoHelper, error) {
	pickleKey := []byte(c.config.PickleKey)
	dbPath := "matrix_crypto.db"

	helper, err := cryptohelper.NewCryptoHelper(c.client, pickleKey, dbPath)
	if err != nil {
		return nil, fmt.Errorf("NewCryptoHelper failed: %w", err)
	}

	c.logger.Info("Initializing crypto helper database...")
	if err := helper.Init(context.Background()); err != nil {
		return nil, fmt.Errorf("CryptoHelper Init failed: %w", err)
	}
	c.logger.Info("Crypto helper database initialized")

	return helper, nil
}

func (c *Client) verifyWithRecoveryKey(machine *crypto.OlmMachine) error {
	ctx := context.Background()

	c.logger.Info("Getting default key data from SSSS...")
	keyId, keyData, err := machine.SSSS.GetDefaultKeyData(ctx)
	if err != nil {
		return fmt.Errorf("failed to get default key data: %w", err)
	}

	c.logger.Info("Verifying recovery key...")
	key, err := keyData.VerifyRecoveryKey(keyId, c.config.RecoveryKey)
	if err != nil {
		return fmt.Errorf("failed to verify recovery key: %w", err)
	}

	c.logger.Info("Fetching cross-signing keys from SSSS...")
	if err := machine.FetchCrossSigningKeysFromSSSS(ctx, key); err != nil {
		return fmt.Errorf("failed to fetch cross-signing keys: %w", err)
	}

	c.logger.Info("Signing own device...")
	if err := machine.SignOwnDevice(ctx, machine.OwnIdentity()); err != nil {
		return fmt.Errorf("failed to sign own device: %w", err)
	}

	c.logger.Info("Signing own master key...")
	if err := machine.SignOwnMasterKey(ctx); err != nil {
		return fmt.Errorf("failed to sign own master key: %w", err)
	}

	c.logger.Info("Device verification with recovery key completed successfully")
	return nil
}

func (c *Client) shouldRequestSession(sessionID string) bool {
	c.requestedSessionMutex.Lock()
	defer c.requestedSessionMutex.Unlock()

	info, exists := c.requestedSessions[sessionID]
	if exists {
		// Exponential backoff: 30s, 60s, 120s, 240s, then cap at 5 minutes
		backoffDurations := []time.Duration{
			30 * time.Second,
			60 * time.Second,
			120 * time.Second,
			240 * time.Second,
			300 * time.Second,
		}

		backoffIndex := info.retryCount
		if backoffIndex >= len(backoffDurations) {
			backoffIndex = len(backoffDurations) - 1
		}

		minWait := backoffDurations[backoffIndex]
		if time.Since(info.lastRequested) < minWait {
			c.logger.Debug("Session was requested recently, not requesting again",
				"session_id", sessionID,
				"retry_count", info.retryCount,
				"min_wait_seconds", minWait.Seconds(),
				"elapsed_seconds", time.Since(info.lastRequested).Seconds())
			return false
		}

		info.retryCount++
		info.lastRequested = time.Now()
	} else {
		c.requestedSessions[sessionID] = &sessionRequestInfo{
			lastRequested: time.Now(),
			retryCount:    0,
		}
	}

	// Clean up old entries
	for sid, info := range c.requestedSessions {
		if time.Since(info.lastRequested) > 10*time.Minute {
			delete(c.requestedSessions, sid)
		}
	}

	return true
}

func (c *Client) attemptDecryption(ctx context.Context, evt *event.Event) (*event.Event, error) {
	if evt.Content.Parsed != nil {
		decrypted, err := c.cryptoHelper.Decrypt(ctx, evt)
		if err == nil {
			return decrypted, nil
		}

		c.logger.Debug("Direct decryption failed: %v", err)

		if enc, ok := evt.Content.Parsed.(*event.EncryptedEventContent); ok {
			if strings.Contains(err.Error(), "no session with given ID found") {
				c.logger.Info("Missing megolm session detected, will attempt to request it: session_id=%s, room_id=%s, sender_key=%s, event_id=%s",
					enc.SessionID, evt.RoomID, enc.SenderKey, evt.ID)
				if c.shouldRequestSession(string(enc.SessionID)) {
					c.logger.Info("Requesting missing megolm session from other devices: session_id=%s, room_id=%s", enc.SessionID, evt.RoomID)
					machine := c.cryptoHelper.Machine()
					requestID := fmt.Sprintf("%s-%s-%d", evt.RoomID, enc.SessionID, time.Now().UnixNano())
					if keyReqErr := machine.SendRoomKeyRequest(ctx, evt.RoomID, enc.SenderKey, enc.SessionID, requestID, nil); keyReqErr != nil {
						c.logger.Error("Failed to send room key request: %v", keyReqErr)
					} else {
						c.logger.Info("Room key request sent, waiting for response: request_id=%s, session_id=%s", requestID, enc.SessionID)
						waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
						defer cancel()
						c.logger.Debug("Waiting for session to arrive: session_id=%s, room_id=%s", enc.SessionID, evt.RoomID)
						if found := machine.WaitForSession(waitCtx, evt.RoomID, enc.SenderKey, enc.SessionID, 30*time.Second); found {
							c.logger.Info("Session received successfully, attempting decryption: session_id=%s", enc.SessionID)
							if decryptedAfterSess, decryptErr := c.cryptoHelper.Decrypt(ctx, evt); decryptErr == nil {
								c.logger.Info("Successfully decrypted event after receiving session: session_id=%s, event_id=%s", enc.SessionID, evt.ID)
								return decryptedAfterSess, nil
							} else {
								c.logger.Error("Failed to decrypt even after receiving session: %v", decryptErr)
							}
						} else {
							c.logger.Debug("Session did not arrive within timeout: session_id=%s", enc.SessionID)
						}
					}
				}
			}
		}
	}

	evtCopy := *evt
	mapBytes, err := json.Marshal(evtCopy.Content.Raw)
	if err != nil {
		return nil, fmt.Errorf("marshal content for re-parse failed: %w", err)
	}

	var encContent event.EncryptedEventContent
	if err := json.Unmarshal(mapBytes, &encContent); err != nil {
		return nil, fmt.Errorf("unmarshal content for re-parse failed: %w", err)
	}

	evtCopy.Content.Parsed = &encContent
	decrypted, err := c.cryptoHelper.Decrypt(ctx, &evtCopy)
	if err == nil {
		return decrypted, nil
	}

	c.logger.Info("Missing megolm session after reparse, will attempt to request it: session_id=%s, room_id=%s, sender_key=%s, event_id=%s",
		encContent.SessionID, evtCopy.RoomID, encContent.SenderKey, evtCopy.ID)

	if c.shouldRequestSession(string(encContent.SessionID)) {
		c.logger.Info("Requesting missing megolm session (after reparse) from other devices: session_id=%s, room_id=%s", encContent.SessionID, evtCopy.RoomID)
		machine := c.cryptoHelper.Machine()
		requestID := fmt.Sprintf("%s-%s-%d", evtCopy.RoomID, encContent.SessionID, time.Now().UnixNano())

		if reqErr := machine.SendRoomKeyRequest(ctx, evtCopy.RoomID, encContent.SenderKey, encContent.SessionID, requestID, nil); reqErr != nil {
			c.logger.Error("Failed to request session after reparsing: %v", reqErr)
		} else {
			c.logger.Debug("Session request sent after reparsing: request_id=%s", requestID)
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			c.logger.Debug("Waiting for session to arrive after reparsing: session_id=%s, room_id=%s", encContent.SessionID, evtCopy.RoomID)
			if found := machine.WaitForSession(waitCtx, evtCopy.RoomID, encContent.SenderKey, encContent.SessionID, 30*time.Second); found {
				c.logger.Info("Session received successfully (after reparse), attempting decryption: session_id=%s", encContent.SessionID)
				if decryptedAfterSessReparse, decryptErr := c.cryptoHelper.Decrypt(ctx, &evtCopy); decryptErr == nil {
					c.logger.Info("Successfully decrypted event after receiving session (reparse): session_id=%s, event_id=%s", encContent.SessionID, evtCopy.ID)
					return decryptedAfterSessReparse, nil
				} else {
					c.logger.Error("Failed to decrypt even after receiving session (reparse): %v", decryptErr)
				}
			} else {
				c.logger.Info("Session did not arrive within timeout (reparse): session_id=%s", encContent.SessionID)
			}
		}
	}

	return nil, err
}

func (c *Client) processEvent(ctx context.Context, evt *event.Event) {
	if string(evt.RoomID) != c.roomID {
		return
	}

	if evt.Type == event.EventEncrypted {
		if evt.RoomID == "" {
			evt.RoomID = id.RoomID(c.roomID)
		}

		decryptedEvt, err := c.attemptDecryption(ctx, evt)
		if err != nil {
			c.logger.Error("Failed to decrypt event after all attempts: %v", err)

			if evt.Content.Parsed != nil {
				if enc, ok := evt.Content.Parsed.(*event.EncryptedEventContent); ok {
					c.logger.Warn("Decryption failed for encrypted event: algorithm=%s, sender_key=%s, session_id=%s, event_id=%s, room_id=%s. This usually means the session was not received from other devices.",
						enc.Algorithm, enc.SenderKey, enc.SessionID, evt.ID, evt.RoomID)
					c.logger.Info("To fix this, ensure other devices in the room are online and have the session. The session may be requested again after backoff period.")
				}
			}
			return
		}

		*evt = *decryptedEvt
	}

	if evt.Type == event.EventMessage {
		messageContent := evt.Content.AsMessage()
		if messageContent == nil {
			c.logger.Debug("Event type is EventMessage but content is not a valid message: event_id=%s, room_id=%s",
				evt.ID, evt.RoomID)
			return
		}

		mentionsMe := false
		c.logger.Info("=== MATRIX MESSAGE RECEIVED === sender=%s, room_id=%s, body=%s, msgtype=%s, event_id=%s",
			evt.Sender, evt.RoomID, messageContent.Body, messageContent.MsgType, evt.ID)

		if messageContent.Mentions != nil {
			for _, userID := range messageContent.Mentions.UserIDs {
				if userID == id.UserID(c.config.UserID) {
					mentionsMe = true
				}
			}
		}

		if !mentionsMe {
			c.logger.Debug("Message not directed at bot, ignoring")
			return
		}

		body := c.mentionRegex.ReplaceAllString(messageContent.Body, "$1")

		senderID := string(evt.Sender)
		username := senderID
		if idx := strings.Index(username, ":"); idx > 0 {
			username = username[1:idx]
		}

		c.logger.Info("Processing message from user: username=%s, sender_id=%s, message=%s",
			username, senderID, body)

		if c.messageHandler != nil {
			c.messageHandler.HandleMessage(evt.RoomID, evt.Sender, body)
		}
	}
}

func (c *Client) SendMessage(message string) error {
	c.logger.Info("Sending message to Matrix room %s", c.roomID)

	content := event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          message,
		Format:        event.FormatHTML,
		FormattedBody: formatMessage(message),
	}

	_, err := c.client.SendMessageEvent(context.Background(), id.RoomID(c.roomID), event.EventMessage, content)
	if err != nil {
		c.logger.Error("Failed to send message to Matrix: %v", err)
		return fmt.Errorf("failed to send message: %w", err)
	}

	c.logger.Info("Message sent to Matrix successfully")
	return nil
}

// formatMessage converts markdown to HTML for Matrix formatting
func formatMessage(message string) string {
	// Create markdown parser with extensions
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse([]byte(message))

	// Create HTML renderer with extensions
	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	return string(markdown.Render(doc, renderer))
}
