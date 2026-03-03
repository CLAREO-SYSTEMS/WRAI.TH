package relay

import (
	"context"
	"net/http"
)

type contextKey string

const agentNameKey contextKey = "agent_name"
const projectKey contextKey = "project_name"

// HTTPContextFunc extracts the project from the ?project= query parameter
// and the optional ?agent= fallback, injecting both into the request context.
// Agent identity is primarily set via register_agent + the "as" param on tool calls.
func HTTPContextFunc(ctx context.Context, r *http.Request) context.Context {
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		agent = "anonymous"
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		project = "default"
	}
	ctx = context.WithValue(ctx, agentNameKey, agent)
	return context.WithValue(ctx, projectKey, project)
}

// AgentFromContext retrieves the agent name from the context.
func AgentFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(agentNameKey).(string); ok {
		return v
	}
	return "anonymous"
}

// ProjectFromContext retrieves the project name from the context.
func ProjectFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(projectKey).(string); ok {
		return v
	}
	return "default"
}
