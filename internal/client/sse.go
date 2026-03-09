package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SSEEvent represents a parsed event from the relay activity stream.
type SSEEvent struct {
	Type    string                 `json:"type"`    // message, task, activity, agent_status, memory, register, goal, conversation
	Action  string                 `json:"action"`  // e.g. dispatch, claim, set, conflict
	Agent   string                 `json:"agent"`   // source agent
	Project string                 `json:"project"`
	Target  string                 `json:"target"`  // target agent or profile
	Label   string                 `json:"label"`
	Data    map[string]interface{} `json:"data"`    // full event payload
	TS      int64                  `json:"ts"`
}

// EventHandler processes an SSE event.
type EventHandler func(SSEEvent)

// SSEClient maintains a persistent connection to the relay activity stream.
type SSEClient struct {
	url            string
	project        string
	apiKey         string
	reconnectDelay time.Duration
	handlers       map[string][]EventHandler
}

// NewSSEClient creates an SSE client.
func NewSSEClient(baseURL, project, apiKey string, reconnectDelay time.Duration) *SSEClient {
	return &SSEClient{
		url:            baseURL,
		project:        project,
		apiKey:         apiKey,
		reconnectDelay: reconnectDelay,
		handlers:       make(map[string][]EventHandler),
	}
}

// On registers a handler for an event type.
// Use "*" to handle all events.
func (s *SSEClient) On(eventType string, handler EventHandler) {
	s.handlers[eventType] = append(s.handlers[eventType], handler)
}

// Run connects to the SSE stream and dispatches events.
// Reconnects automatically on disconnect.
func (s *SSEClient) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := s.connect(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("[sse] disconnected: %v, reconnecting in %v", err, s.reconnectDelay)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.reconnectDelay):
		}
	}
}

func (s *SSEClient) connect(ctx context.Context) error {
	u := fmt.Sprintf("%s/api/activity/stream?project=%s", s.url, url.QueryEscape(s.project))

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	client := &http.Client{Timeout: 0} // no timeout for SSE
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	log.Printf("[sse] connected to %s", u)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256KB buffer

	var eventType string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event
			if eventType != "" && len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				s.dispatch(eventType, data)
			}
			eventType = ""
			dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return fmt.Errorf("stream closed")
}

func (s *SSEClient) dispatch(eventType, data string) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		log.Printf("[sse] invalid JSON in %s event: %v", eventType, err)
		return
	}

	evt := SSEEvent{
		Type: eventType,
		Data: raw,
	}

	// Extract common fields
	if v, ok := raw["action"].(string); ok {
		evt.Action = v
	}
	if v, ok := raw["agent"].(string); ok {
		evt.Agent = v
	}
	if v, ok := raw["project"].(string); ok {
		evt.Project = v
	}
	if v, ok := raw["target"].(string); ok {
		evt.Target = v
	}
	if v, ok := raw["label"].(string); ok {
		evt.Label = v
	}
	if v, ok := raw["ts"].(float64); ok {
		evt.TS = int64(v)
	}

	// Dispatch to typed handlers
	if handlers, ok := s.handlers[eventType]; ok {
		for _, h := range handlers {
			h(evt)
		}
	}

	// Dispatch to wildcard handlers
	if handlers, ok := s.handlers["*"]; ok {
		for _, h := range handlers {
			h(evt)
		}
	}
}
