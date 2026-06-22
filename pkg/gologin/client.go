package gologin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin HTTP client for the GoLogin launcher sidecar.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a GoLogin client targeting the given launcher base URL.
func New(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Start launches the GoLogin browser profile and returns the Playwright CDP WebSocket URL.
func (c *Client) Start(profileID string) (string, error) {
	url := fmt.Sprintf("%s/start/%s", c.baseURL, profileID)
	resp, err := c.httpClient.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("gologin start %s: %w", profileID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gologin start %s: unexpected status %d: %s", profileID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		WsURL string `json:"wsUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("gologin start %s: decode response: %w", profileID, err)
	}
	if result.WsURL == "" {
		return "", fmt.Errorf("gologin start %s: empty wsUrl in response", profileID)
	}
	return result.WsURL, nil
}

// Stop signals the GoLogin launcher to close the browser profile.
func (c *Client) Stop(profileID string) error {
	url := fmt.Sprintf("%s/stop/%s", c.baseURL, profileID)
	resp, err := c.httpClient.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("gologin stop %s: %w", profileID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gologin stop %s: unexpected status %d", profileID, resp.StatusCode)
	}
	return nil
}
