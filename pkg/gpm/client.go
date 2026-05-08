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

	// Retry start profile up to 5 times - GPM sometimes needs time to launch browser
	var debugAddr string
	maxRetries := 5
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := c.client.Get(url)
		if err != nil {
			if attempt == maxRetries {
				return "", fmt.Errorf("failed to start profile: %w", err)
			}
			time.Sleep(3 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("GPM API returned status %d: %s", resp.StatusCode, string(body))
		}

		// Log raw response to debug
		c.log.Sugar().Infof("GPM raw response: %s", string(body))

		var result StartProfileResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return "", fmt.Errorf("failed to decode response: %w", err)
		}

		// Try remote_debugging_address first, fallback to ws_endpoint
		debugAddr = result.Data.RemoteDebuggingAddress
		if debugAddr == "" && result.Data.WSEndpoint != "" {
			// ws_endpoint is already a full ws:// URL, return directly
			c.log.Sugar().Infof("GPM profile %s started (via ws_endpoint)", profileID[:8])
			return result.Data.WSEndpoint, nil
		}
		if debugAddr != "" {
			break
		}

		// remote_debugging_address empty - browser still starting, wait and retry
		c.log.Sugar().Warnf("GPM profile %s: empty debug address (attempt %d/%d), waiting...", profileID[:8], attempt, maxRetries)
		time.Sleep(5 * time.Second)
	}

	if debugAddr == "" {
		return "", fmt.Errorf("empty remote_debugging_address after %d attempts", maxRetries)
	}

	// Get WebSocket endpoint from CDP
	wsURL, err := c.getWebSocketURL(debugAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get WebSocket URL: %w", err)
	}

	c.log.Sugar().Infof("GPM profile %s started", profileID[:8])
	return wsURL, nil
}

func (c *Client) getWebSocketURL(debugAddr string) (string, error) {
	// Query CDP /json/version to get webSocketDebuggerUrl
	url := fmt.Sprintf("http://%s/json/version", debugAddr)

	// Retry up to 5 times with 2 second delay (total ~10 seconds)
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		} else {
			// First attempt: wait 2 seconds for browser to start
			time.Sleep(2 * time.Second)
		}

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

		return wsURL, nil
	}

	return "", fmt.Errorf("failed to get WebSocket URL after %d attempts", maxRetries)
}

func (c *Client) StopProfile(profileID string) error {
	url := fmt.Sprintf("%s/profiles/close/%s", c.apiURL, profileID)

	resp, err := c.client.Get(url)
	if err != nil {
		c.log.Sugar().Warnf("Failed to stop profile %s: %v", profileID[:8], err)
		return fmt.Errorf("failed to stop profile: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		c.log.Sugar().Warnf("GPM stop profile %s returned %d: %s", profileID[:8], resp.StatusCode, string(body))
		return fmt.Errorf("GPM stop returned status %d", resp.StatusCode)
	}

	c.log.Sugar().Infof("GPM profile %s stopped", profileID[:8])
	return nil
}
