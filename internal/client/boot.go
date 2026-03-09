package client

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BuildBootPrompt constructs the initial prompt for an agent's first turn.
// Seeds the agent with its full session context so it doesn't boot blind.
func BuildBootPrompt(name string, cfg AgentConfig, ctx *SessionContext, inbox []Message) string {
	var b strings.Builder

	fmt.Fprintf(&b, "You are agent %q.\n\n", name)

	// Profile context (from soul)
	if ctx != nil && len(ctx.Profile) > 0 && string(ctx.Profile) != "null" {
		var profile struct {
			Role        string `json:"role"`
			ContextPack string `json:"context_pack"`
		}
		if err := json.Unmarshal(ctx.Profile, &profile); err == nil && profile.Role != "" {
			fmt.Fprintf(&b, "## Role\n%s\n\n", profile.Role)
			if profile.ContextPack != "" {
				fmt.Fprintf(&b, "## Context\n%s\n\n", profile.ContextPack)
			}
		}
	}

	// Pending tasks
	if ctx != nil && len(ctx.PendingTasks) > 0 && string(ctx.PendingTasks) != "null" {
		var tasks struct {
			AssignedToMe []json.RawMessage `json:"assigned_to_me"`
			DispatchedByMe []json.RawMessage `json:"dispatched_by_me"`
		}
		if err := json.Unmarshal(ctx.PendingTasks, &tasks); err == nil {
			total := len(tasks.AssignedToMe) + len(tasks.DispatchedByMe)
			if total > 0 {
				fmt.Fprintf(&b, "## Pending Tasks (%d)\n", total)
				for _, t := range tasks.AssignedToMe {
					fmt.Fprintf(&b, "- [assigned] %s\n", summarizeTask(t))
				}
				for _, t := range tasks.DispatchedByMe {
					fmt.Fprintf(&b, "- [dispatched] %s\n", summarizeTask(t))
				}
				b.WriteString("\n")
			}
		}
	}

	// Unread messages (budget-pruned)
	if len(inbox) > 0 {
		fmt.Fprintf(&b, "## Unread Messages (%d, budget-pruned)\n", len(inbox))
		for _, msg := range inbox {
			priority := msg.Priority
			if priority == "" {
				priority = "P2"
			}
			fmt.Fprintf(&b, "- [%s] From %s: %s\n", priority, msg.From, truncateStr(msg.Subject, 80))
		}
		b.WriteString("\n")
	}

	// Relevant memories
	if ctx != nil && len(ctx.Memories) > 0 && string(ctx.Memories) != "null" && string(ctx.Memories) != "[]" {
		fmt.Fprintf(&b, "## Relevant Memories\n%s\n\n", string(ctx.Memories))
	}

	// Instructions
	b.WriteString("Register with the relay using your profile, then process your inbox with /relay talk.\n")
	b.WriteString("When done, stay ready — you'll be resumed with --resume for the next turn.\n")

	return b.String()
}

// BuildWakePrompt constructs the prompt for a resumed turn.
func BuildWakePrompt(reason string, inbox []Message) string {
	var b strings.Builder

	if reason != "" {
		fmt.Fprintf(&b, "Wake reason: %s\n\n", reason)
	}

	if len(inbox) > 0 {
		fmt.Fprintf(&b, "You have %d unread message(s).\n", len(inbox))
	}

	b.WriteString("Run /relay talk to process your inbox.")
	return b.String()
}

func summarizeTask(raw json.RawMessage) string {
	var t struct {
		Title    string `json:"title"`
		Priority string `json:"priority"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return string(raw)
	}
	p := t.Priority
	if p == "" {
		p = "P2"
	}
	return fmt.Sprintf("[%s/%s] %s", p, t.Status, t.Title)
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
