package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// RelayClient wraps HTTP calls to the WRAI.TH relay server.
// Uses REST for reads, MCP JSON-RPC for writes (supports the `as` parameter).
type RelayClient struct {
	baseURL    string
	project    string
	apiKey     string
	httpClient *http.Client
}

// NewRelayClient creates a relay client from config.
func NewRelayClient(cfg RelayConfig) *RelayClient {
	return &RelayClient{
		baseURL: cfg.URL,
		project: cfg.Project,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// --- MCP JSON-RPC (writes) ---

type mcpRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *RelayClient) mcpCall(tool string, args map[string]interface{}) (json.RawMessage, error) {
	params := map[string]interface{}{
		"name":      tool,
		"arguments": args,
	}
	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  params,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp request: %w", err)
	}

	mcpURL := fmt.Sprintf("%s/mcp?project=%s", r.baseURL, url.QueryEscape(r.project))
	httpReq, err := http.NewRequest("POST", mcpURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp call %s: %w", tool, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mcp call %s: HTTP %d: %s", tool, resp.StatusCode, string(respBody))
	}

	var mcpResp mcpResponse
	if err := json.NewDecoder(resp.Body).Decode(&mcpResp); err != nil {
		return nil, fmt.Errorf("decode mcp response: %w", err)
	}
	if mcpResp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	return mcpResp.Result, nil
}

// --- REST (reads) ---

func (r *RelayClient) restGet(path string, query url.Values) (json.RawMessage, error) {
	u := fmt.Sprintf("%s%s", r.baseURL, path)
	if query == nil {
		query = url.Values{}
	}
	query.Set("project", r.project)
	u += "?" + query.Encode()

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, string(body))
	}

	return json.RawMessage(body), nil
}

// --- High-level methods ---

// RegisterOpts are the options for registering an agent.
type RegisterOpts struct {
	Name            string
	Role            string
	Description     string
	ReportsTo       string
	ProfileSlug     string
	IsExecutive     bool
	SessionID       string
	InterestTags    []string
	MaxContextBytes int
}

// SessionContext is the response from register_agent or get_session_context.
type SessionContext struct {
	Profile         json.RawMessage `json:"profile"`
	PendingTasks    json.RawMessage `json:"pending_tasks"`
	UnreadMessages  json.RawMessage `json:"unread_messages"`
	Conversations   json.RawMessage `json:"active_conversations"`
	Memories        json.RawMessage `json:"relevant_memories"`
}

// RegisterAgent registers an agent and returns its session context.
func (r *RelayClient) RegisterAgent(opts RegisterOpts) (*SessionContext, error) {
	args := map[string]interface{}{
		"name": opts.Name,
	}
	if opts.Role != "" {
		args["role"] = opts.Role
	}
	if opts.Description != "" {
		args["description"] = opts.Description
	}
	if opts.ReportsTo != "" {
		args["reports_to"] = opts.ReportsTo
	}
	if opts.ProfileSlug != "" {
		args["profile_slug"] = opts.ProfileSlug
	}
	if opts.IsExecutive {
		args["is_executive"] = true
	}
	if opts.SessionID != "" {
		args["session_id"] = opts.SessionID
	}
	if len(opts.InterestTags) > 0 {
		tagsJSON, _ := json.Marshal(opts.InterestTags)
		args["interest_tags"] = string(tagsJSON)
	}
	if opts.MaxContextBytes > 0 {
		args["max_context_bytes"] = opts.MaxContextBytes
	}

	result, err := r.mcpCall("register_agent", args)
	if err != nil {
		return nil, err
	}

	var resp struct {
		SessionContext SessionContext `json:"session_context"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parse register response: %w", err)
	}
	return &resp.SessionContext, nil
}

// GetSessionContext returns the full boot context for an agent.
func (r *RelayClient) GetSessionContext(agent string) (*SessionContext, error) {
	args := map[string]interface{}{
		"as": agent,
	}
	result, err := r.mcpCall("get_session_context", args)
	if err != nil {
		return nil, err
	}

	var ctx SessionContext
	if err := json.Unmarshal(result, &ctx); err != nil {
		return nil, fmt.Errorf("parse session context: %w", err)
	}
	return &ctx, nil
}

// Message represents a relay message.
type Message struct {
	ID             string `json:"id"`
	From           string `json:"from"`
	To             string `json:"to"`
	Subject        string `json:"subject"`
	Content        string `json:"content"`
	Type           string `json:"type"`
	Priority       string `json:"priority"`
	ConversationID string `json:"conversation_id"`
	CreatedAt      string `json:"created_at"`
}

// GetInbox returns unread messages for an agent.
func (r *RelayClient) GetInbox(agent string, applyBudget bool) ([]Message, error) {
	args := map[string]interface{}{
		"as":           agent,
		"unread_only":  true,
		"full_content": true,
	}
	if applyBudget {
		args["apply_budget"] = true
	}

	result, err := r.mcpCall("get_inbox", args)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parse inbox: %w", err)
	}
	return resp.Messages, nil
}

// SleepAgent marks an agent as sleeping in the relay.
func (r *RelayClient) SleepAgent(agent string) error {
	args := map[string]interface{}{
		"as": agent,
	}
	_, err := r.mcpCall("sleep_agent", args)
	return err
}

// SendMessage sends a message through the relay.
func (r *RelayClient) SendMessage(as, to, subject, content, msgType string, priority string) error {
	args := map[string]interface{}{
		"as":      as,
		"to":      to,
		"subject": subject,
		"content": content,
		"type":    msgType,
	}
	if priority != "" {
		args["priority"] = priority
	}
	_, err := r.mcpCall("send_message", args)
	return err
}

// AckDelivery acknowledges receipt of a message.
func (r *RelayClient) AckDelivery(agent, messageID string) error {
	args := map[string]interface{}{
		"as":         agent,
		"message_id": messageID,
	}
	_, err := r.mcpCall("ack_delivery", args)
	return err
}

// MarkRead marks messages as read.
func (r *RelayClient) MarkRead(agent string, messageIDs []string) error {
	args := map[string]interface{}{
		"as":          agent,
		"message_ids": messageIDs,
	}
	_, err := r.mcpCall("mark_read", args)
	return err
}

// QueryContext retrieves relevant context for an agent's task.
func (r *RelayClient) QueryContext(agent, query string) (json.RawMessage, error) {
	args := map[string]interface{}{
		"as":    agent,
		"query": query,
	}
	return r.mcpCall("query_context", args)
}

// ListAgents returns all registered agents.
func (r *RelayClient) ListAgents() (json.RawMessage, error) {
	return r.restGet("/api/agents", nil)
}
