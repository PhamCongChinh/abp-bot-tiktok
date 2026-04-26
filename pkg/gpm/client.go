package gpm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

type Client struct {
	apiURL string
	log    *zap.Logger
	client *http.Client
}

type StartProfileResponse struct {
	Data struct {
		RemoteDebuggingAddress string `json:"remote_debugging_address"`
		WSEndpoint             string `json:"ws_endpoint"`
	} `json:"data"`
}

func NewClient(apiURL string, log *zap.Logger) *Client {
	return &Client{
		apiURL: apiURL,
		log:    log,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) StartProfile(profileID string) (string, error) {
	url := fmt.Sprintf("%s/profiles/start/%s", c.apiURL, profileID)
	c.log.Info("Starting GPM profile", zap.String("url", url))

	resp, err := c.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to start profile: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GPM API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Log raw response for debugging
	c.log.Info("GPM API response", zap.String("body", string(body)))

	var result StartProfileResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	debugAddr := result.Data.RemoteDebuggingAddress
	if debugAddr == "" {
		c.log.Warn("remote_debugging_address is empty")
		return "", fmt.Errorf("empty remote_debugging_address in response")
	}

	// Get WebSocket endpoint from CDP
	wsURL, err := c.getWebSocketURL(debugAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get WebSocket URL: %w", err)
	}

	c.log.Info("GPM profile started", zap.String("debug_addr", debugAddr), zap.String("ws_url", wsURL))
	return wsURL, nil
}

func (c *Client) getWebSocketURL(debugAddr string) (string, error) {
	// Query CDP /json/version to get webSocketDebuggerUrl
	url := fmt.Sprintf("http://%s/json/version", debugAddr)

	// Retry up to 5 times with 2 second delay (total ~10 seconds)
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			c.log.Info("Retrying CDP connection", zap.Int("attempt", i+1), zap.Int("max", maxRetries))
			time.Sleep(2 * time.Second)
		} else {
			// First attempt: wait 2 seconds for browser to start
			c.log.Info("Waiting for GPM browser to start...")
			time.Sleep(2 * time.Second)
		}

		c.log.Info("Querying CDP endpoint", zap.String("url", url), zap.Int("attempt", i+1))

		resp, err := c.client.Get(url)
		if err != nil {
			if i == maxRetries-1 {
				return "", fmt.Errorf("failed to query CDP after %d attempts: %w", maxRetries, err)
			}
			continue
		}
		defer resp.Body.Close()

		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", fmt.Errorf("failed to decode CDP response: %w", err)
		}

		wsURL, ok := result["webSocketDebuggerUrl"].(string)
		if !ok || wsURL == "" {
			return "", fmt.Errorf("webSocketDebuggerUrl not found in CDP response")
		}

		c.log.Info("Got WebSocket URL from CDP", zap.String("ws_url", wsURL))
		return wsURL, nil
	}

	return "", fmt.Errorf("failed to get WebSocket URL after %d attempts", maxRetries)
}

func (c *Client) StopProfile(profileID string) error {
	url := fmt.Sprintf("%s/profiles/close/%s", c.apiURL, profileID)
	c.log.Info("Stopping GPM profile", zap.String("url", url))

	resp, err := c.client.Get(url)
	if err != nil {
		c.log.Warn("Failed to stop profile (network error)", zap.Error(err))
		return fmt.Errorf("failed to stop profile: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		c.log.Warn("GPM stop returned non-200", 
			zap.Int("status", resp.StatusCode), 
			zap.String("body", string(body)),
		)
		return fmt.Errorf("GPM stop returned status %d", resp.StatusCode)
	}

	c.log.Info("✅ GPM profile stopped successfully")
	return nil
}
