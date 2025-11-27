package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/itchyny/gojq"
	"github.com/mule-ai/mule/matrix-microservice/internal/config"
	"github.com/mule-ai/mule/matrix-microservice/internal/logger"
)

type Dispatcher struct {
	config *config.WebhookConfig
	client *http.Client
	logger *logger.Logger
}

func New(cfg *config.WebhookConfig, logger *logger.Logger) *Dispatcher {
	logger.Info("Initializing webhook dispatcher")
	logger.Debug("Default webhook: %s", cfg.Default)
	logger.Debug("Number of command webhooks: %d", len(cfg.Commands))

	return &Dispatcher{
		config: cfg,
		client: &http.Client{
			Timeout: time.Duration(cfg.Timeout) * time.Second,
		},
		logger: logger,
	}
}

func (d *Dispatcher) Dispatch(message string, command string) (string, error) {
	d.logger.Info("Dispatching webhook for message: %s", message)
	d.logger.Debug("Command extracted: %s", command)

	var webhookURL string
	var tpl string
	var authToken string
	var jqSelector string

	// Determine which webhook to use
	if command != "" {
		if url, exists := d.config.Commands[command]; exists {
			webhookURL = url

			// Use command-specific template if available, otherwise use default
			if cmdTpl, exists := d.config.CommandTemplates[command]; exists {
				tpl = cmdTpl
				d.logger.Debug("Using command-specific template for: %s", command)
			} else {
				tpl = d.config.Template
			}

			d.logger.Info("Using command webhook: %s for command: %s", url, command)

			// Get auth token for this command
			if token, exists := d.config.AuthTokens[command]; exists {
				authToken = token
				d.logger.Debug("Using auth token for command: %s", command)
			} else if d.config.DefaultAuth != "" {
				if token, exists := d.config.AuthTokens[d.config.DefaultAuth]; exists {
					authToken = token
					d.logger.Debug("Using default auth token for command: %s", command)
				}
			}

			// Get JQ selector for this command
			if selector, exists := d.config.CommandSelectors[command]; exists {
				jqSelector = selector
				d.logger.Debug("Using JQ selector for command: %s", command)
			}
		} else {
			// Command not found, use default
			webhookURL = d.config.Default
			tpl = d.config.Template
			d.logger.Warn("Command %s not found, using default webhook: %s", command, webhookURL)

			// Get default auth token
			if d.config.DefaultAuth != "" {
				if token, exists := d.config.AuthTokens[d.config.DefaultAuth]; exists {
					authToken = token
					d.logger.Debug("Using default auth token")
				}
			}
		}
	} else {
		// No command, use default webhook
		webhookURL = d.config.Default
		tpl = d.config.Template
		d.logger.Info("Using default webhook: %s", webhookURL)

		// Get default auth token
		if d.config.DefaultAuth != "" {
			if token, exists := d.config.AuthTokens[d.config.DefaultAuth]; exists {
				authToken = token
				d.logger.Debug("Using default auth token")
			}
		}
	}

	// Use default JQ selector if not set for command
	if jqSelector == "" {
		jqSelector = d.config.JQSelector
		d.logger.Debug("Using default JQ selector: %s", jqSelector)
	}

	// Render template with message
	d.logger.Debug("Rendering template with message")
	tmpl, err := template.New("webhook").Parse(tpl)
	if err != nil {
		d.logger.Error("Failed to parse template: %v", err)
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]string{"MESSAGE": message})
	if err != nil {
		d.logger.Error("Failed to execute template: %v", err)
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	// Create HTTP request
	d.logger.Info("Sending HTTP POST request to: %s (Message length: %d bytes, Has auth: %v)",
		webhookURL, buf.Len(), authToken != "")
	req, err := http.NewRequest("POST", webhookURL, &buf)
	if err != nil {
		d.logger.Error("Failed to create request: %v (URL: %s)", err, webhookURL)
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add authorization header if token is provided
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
		d.logger.Debug("Added authorization header to request (token prefix: %s...)", authToken[:min(10, len(authToken))])
	}

	// Send HTTP request
	startTime := time.Now()
	resp, err := d.client.Do(req)
	duration := time.Since(startTime)

	if err != nil {
		d.logger.Error("Failed to send webhook: %v (URL: %s, Duration: %v)", err, webhookURL, duration)
		return "", fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	d.logger.Info("Webhook response status: %d (URL: %s, Duration: %v)", resp.StatusCode, webhookURL, duration)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read response body for error details
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// Truncate long response bodies in error message
		if len(bodyStr) > 500 {
			bodyStr = bodyStr[:500] + "... (truncated)"
		}

		d.logger.Error("Webhook returned status code: %d (URL: %s, Response Headers: %v, Response Body: %s)",
			resp.StatusCode, webhookURL, resp.Header, bodyStr)
		return "", fmt.Errorf("webhook returned status code: %d (URL: %s, Duration: %v, Response: %s)",
			resp.StatusCode, webhookURL, duration, bodyStr)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		d.logger.Error("Failed to read response body: %v", err)
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	d.logger.Debug("Webhook response body: %s", string(body))

	// If no JQ selector, return empty string (no reply)
	if jqSelector == "" {
		d.logger.Info("No JQ selector configured, skipping response parsing")
		return "", nil
	}

	// Parse response using JQ
	reply, err := d.parseResponseWithJQ(body, jqSelector)
	if err != nil {
		d.logger.Error("Failed to parse response with JQ: %v", err)
		return "", fmt.Errorf("failed to parse response with JQ: %w", err)
	}

	d.logger.Info("Webhook dispatched successfully, reply: %s", reply)
	return reply, nil
}

func (d *Dispatcher) parseResponseWithJQ(responseBody []byte, selector string) (string, error) {
	// Parse JSON response
	var data interface{}
	if err := json.Unmarshal(responseBody, &data); err != nil {
		return "", fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	// Compile JQ query
	query, err := gojq.Parse(selector)
	if err != nil {
		return "", fmt.Errorf("failed to parse JQ selector: %w", err)
	}

	// Execute query
	iter := query.Run(data)
	var results []string
	
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		
		if err, ok := v.(error); ok {
			return "", fmt.Errorf("JQ execution error: %w", err)
		}
		
		// Convert result to string
		var resultStr string
		switch val := v.(type) {
		case string:
			resultStr = val
		case nil:
			resultStr = ""
		default:
			// For other types, marshal back to JSON
			jsonBytes, err := json.Marshal(val)
			if err != nil {
				return "", fmt.Errorf("failed to marshal result: %w", err)
			}
			resultStr = string(jsonBytes)
		}
		
		results = append(results, resultStr)
	}
	
	// If no results or empty results and skip_empty is true, return empty string
	if len(results) == 0 || (d.config.SkipEmpty && d.allEmpty(results)) {
		return "", nil
	}
	
	// Join multiple results with newline
	return strings.Join(results, "\n"), nil
}

func (d *Dispatcher) allEmpty(results []string) bool {
	for _, r := range results {
		if r != "" {
			return false
		}
	}
	return true
}

func (d *Dispatcher) ExtractCommand(message string) string {
	// Find the first occurrence of "/" in the message
	if idx := strings.Index(message, "/"); idx >= 0 {
		// Extract command name (everything after / until first space or end of string)
		parts := strings.Fields(message[idx+1:])
		if len(parts) > 0 {
			d.logger.Debug("Extracted command: %s", parts[0])
			return parts[0]
		}
	}
	return ""
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}