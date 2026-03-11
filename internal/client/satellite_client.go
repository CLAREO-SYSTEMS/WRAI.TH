// Package client — satellite_client.go is the HTTP client the station uses
// to send commands to satellite machines. Spawn, stop, wake, status, terminal.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SatelliteClient calls satellite HTTP APIs from the station.
type SatelliteClient struct {
	httpClient *http.Client
}

// NewSatelliteClient creates a new satellite client.
func NewSatelliteClient() *SatelliteClient {
	return &SatelliteClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func satelliteURL(sat SatelliteInfo, path string) string {
	return fmt.Sprintf("http://%s:%d%s", sat.Host, sat.Port, path)
}

// Spawn sends a spawn command to a satellite.
func (c *SatelliteClient) Spawn(sat SatelliteInfo, req SpawnRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal spawn request: %w", err)
	}

	resp, err := c.httpClient.Post(satelliteURL(sat, "/api/spawn"), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("satellite %s:%d unreachable: %w", sat.Host, sat.Port, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("satellite spawn failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// Stop sends a stop command to a satellite.
func (c *SatelliteClient) Stop(sat SatelliteInfo, agentName string) error {
	resp, err := c.httpClient.Post(satelliteURL(sat, "/api/stop/"+agentName), "application/json", nil)
	if err != nil {
		return fmt.Errorf("satellite unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("satellite stop failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// Wake sends a wake command with a ready-made prompt to a satellite.
func (c *SatelliteClient) Wake(sat SatelliteInfo, agentName, prompt, reason string) error {
	payload := map[string]string{
		"prompt": prompt,
		"reason": reason,
	}
	body, _ := json.Marshal(payload)

	resp, err := c.httpClient.Post(satelliteURL(sat, "/api/wake/"+agentName), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("satellite unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("satellite wake failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// Status returns the state of all agents on a satellite.
func (c *SatelliteClient) Status(sat SatelliteInfo) (map[string]map[string]interface{}, error) {
	resp, err := c.httpClient.Get(satelliteURL(sat, "/api/status"))
	if err != nil {
		return nil, fmt.Errorf("satellite unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("satellite status HTTP %d", resp.StatusCode)
	}

	var states map[string]map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	return states, nil
}

// Terminal returns terminal output lines for an agent on a satellite.
func (c *SatelliteClient) Terminal(sat SatelliteInfo, agentName string) ([]TerminalLine, error) {
	resp, err := c.httpClient.Get(satelliteURL(sat, "/api/terminal/"+agentName))
	if err != nil {
		return nil, fmt.Errorf("satellite unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("satellite terminal HTTP %d", resp.StatusCode)
	}

	var lines []TerminalLine
	if err := json.NewDecoder(resp.Body).Decode(&lines); err != nil {
		return nil, fmt.Errorf("decode terminal: %w", err)
	}
	return lines, nil
}

// Health checks if a satellite is reachable.
func (c *SatelliteClient) Health(sat SatelliteInfo) error {
	resp, err := c.httpClient.Get(satelliteURL(sat, "/api/health"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("satellite health HTTP %d", resp.StatusCode)
	}
	return nil
}
