package matrix

import (
	"testing"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bold text",
			input:    "**bold text**",
			expected: "<p><strong>bold text</strong></p>\n",
		},
		{
			name:     "Italic text",
			input:    "*italic text*",
			expected: "<p><em>italic text</em></p>\n",
		},
		{
			name:     "Link",
			input:    "[link](https://example.com)",
			expected: "<p><a href=\"https://example.com\" target=\"_blank\">link</a></p>\n",
		},
		{
			name:     "Code block",
			input:    "```go\nfmt.Println(\"hello\")\n```",
			expected: "<pre><code class=\"language-go\">fmt.Println(&quot;hello&quot;)\n</code></pre>\n",
		},
		{
			name:     "Mixed formatting",
			input:    "**bold** and *italic* with [link](https://example.com)",
			expected: "<p><strong>bold</strong> and <em>italic</em> with <a href=\"https://example.com\" target=\"_blank\">link</a></p>\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMessage(tt.input)
			if result != tt.expected {
				t.Errorf("formatMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// Test thread detection helper functions
// These test the logic used in processEvent for detecting replies and threads

func TestThreadDetection(t *testing.T) {
	tests := []struct {
		name               string
		relatesTo          *event.RelatesTo
		expectInReplyTo    bool
		expectThreadRoot   bool
		expectedInReplyTo  id.EventID
		expectedThreadRoot id.EventID
	}{
		{
			name:               "No relatesTo - plain message",
			relatesTo:          nil,
			expectInReplyTo:    false,
			expectThreadRoot:   false,
			expectedInReplyTo:  "",
			expectedThreadRoot: "",
		},
		{
			name: "Message is a reply",
			relatesTo: &event.RelatesTo{
				InReplyTo: &event.InReplyTo{
					EventID: "$replyEvent123",
				},
			},
			expectInReplyTo:    true,
			expectThreadRoot:   false,
			expectedInReplyTo:  "$replyEvent123",
			expectedThreadRoot: "",
		},
		{
			name: "Message is in a thread",
			relatesTo: &event.RelatesTo{
				Type:    event.RelThread,
				EventID: "$threadRoot456",
			},
			expectInReplyTo:    false,
			expectThreadRoot:   true,
			expectedInReplyTo:  "",
			expectedThreadRoot: "$threadRoot456",
		},
		{
			name: "Message is both reply and thread - thread takes precedence",
			relatesTo: &event.RelatesTo{
				Type:    event.RelThread,
				EventID: "$threadRoot789",
				InReplyTo: &event.InReplyTo{
					EventID: "$replyEvent789",
				},
			},
			expectInReplyTo:    true, // Both are set
			expectThreadRoot:   true,
			expectedInReplyTo:  "$replyEvent789",
			expectedThreadRoot: "$threadRoot789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the thread detection logic from processEvent
			var inReplyToEventID id.EventID
			var threadRootEventID id.EventID

			if tt.relatesTo != nil {
				// Check for reply (inReplyTo)
				if tt.relatesTo.GetReplyTo() != "" {
					inReplyToEventID = tt.relatesTo.GetReplyTo()
				}

				// Check for thread (thread parent)
				threadParent := tt.relatesTo.GetThreadParent()
				if threadParent != "" {
					threadRootEventID = threadParent
				}
			}

			// Verify inReplyTo detection
			if tt.expectInReplyTo && inReplyToEventID == "" {
				t.Error("Expected inReplyTo to be set, but it was empty")
			}
			if !tt.expectInReplyTo && inReplyToEventID != "" {
				t.Errorf("Expected inReplyTo to be empty, got %s", inReplyToEventID)
			}
			if tt.expectInReplyTo && inReplyToEventID != tt.expectedInReplyTo {
				t.Errorf("Expected inReplyTo = %s, got %s", tt.expectedInReplyTo, inReplyToEventID)
			}

			// Verify thread root detection
			if tt.expectThreadRoot && threadRootEventID == "" {
				t.Error("Expected threadRootEventID to be set, but it was empty")
			}
			if !tt.expectThreadRoot && threadRootEventID != "" {
				t.Errorf("Expected threadRootEventID to be empty, got %s", threadRootEventID)
			}
			if tt.expectThreadRoot && threadRootEventID != tt.expectedThreadRoot {
				t.Errorf("Expected threadRootEventID = %s, got %s", tt.expectedThreadRoot, threadRootEventID)
			}
		})
	}
}

// Test that RelatesTo.GetThreadParent() works correctly
func TestRelatesToGetThreadParent(t *testing.T) {
	relatesTo := &event.RelatesTo{
		Type:    event.RelThread,
		EventID: "$testThreadRoot",
	}

	parent := relatesTo.GetThreadParent()
	if parent != "$testThreadRoot" {
		t.Errorf("GetThreadParent() = %q, want %q", parent, "$testThreadRoot")
	}
}

// Test that RelatesTo.GetReplyTo() works correctly
func TestRelatesToGetReplyTo(t *testing.T) {
	relatesTo := &event.RelatesTo{
		InReplyTo: &event.InReplyTo{
			EventID: "$testReplyTo",
		},
	}

	replyTo := relatesTo.GetReplyTo()
	if replyTo != "$testReplyTo" {
		t.Errorf("GetReplyTo() = %q, want %q", replyTo, "$testReplyTo")
	}
}

// Test WithReplyTo option function
func TestWithReplyTo(t *testing.T) {
	eventID := id.EventID("$replyEvent123")
	opt := WithReplyTo(eventID)

	opts := &SendMessageOptions{}
	opt(opts)

	if opts.InReplyToEventID != eventID {
		t.Errorf("InReplyToEventID = %q, want %q", opts.InReplyToEventID, eventID)
	}
}

// Test WithMention option function
func TestWithMention(t *testing.T) {
	userID := id.UserID("@testuser:matrix.org")
	opt := WithMention(userID)

	opts := &SendMessageOptions{}
	opt(opts)

	if opts.MentionUserID != userID {
		t.Errorf("MentionUserID = %q, want %q", opts.MentionUserID, userID)
	}
}

// Test combining multiple options
func TestSendMessageOptionsCombined(t *testing.T) {
	replyToID := id.EventID("$replyEvent456")
	mentionID := id.UserID("@mentionuser:matrix.org")

	opts := &SendMessageOptions{}
	WithReplyTo(replyToID)(opts)
	WithMention(mentionID)(opts)

	if opts.InReplyToEventID != replyToID {
		t.Errorf("InReplyToEventID = %q, want %q", opts.InReplyToEventID, replyToID)
	}
	if opts.MentionUserID != mentionID {
		t.Errorf("MentionUserID = %q, want %q", opts.MentionUserID, mentionID)
	}
}

// Test default SendMessageOptions values
func TestSendMessageOptionsDefaults(t *testing.T) {
	opts := &SendMessageOptions{}

	// Default values should be empty
	if opts.InReplyToEventID != "" {
		t.Errorf("Default InReplyToEventID should be empty, got %q", opts.InReplyToEventID)
	}
	if opts.MentionUserID != "" {
		t.Errorf("Default MentionUserID should be empty, got %q", opts.MentionUserID)
	}
}

// Test that reply and mention options can be used with empty values
func TestSendMessageOptionsEmptyValues(t *testing.T) {
	opts := &SendMessageOptions{}

	// Apply empty options - should not change values
	WithReplyTo("")(opts)
	WithMention("")(opts)

	// Values should remain empty
	if opts.InReplyToEventID != "" {
		t.Errorf("InReplyToEventID should remain empty, got %q", opts.InReplyToEventID)
	}
	if opts.MentionUserID != "" {
		t.Errorf("MentionUserID should remain empty, got %q", opts.MentionUserID)
	}
}
