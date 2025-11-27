package matrix

import (
	"testing"
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
