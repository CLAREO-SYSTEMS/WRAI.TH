package discord

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

	"agent-relay/internal/client"
)

// MessageRouter listens to relay SSE events and routes agent messages to Discord.
// SSE-driven (E4) — no polling. Handles conversation→thread mapping (G11)
// and cross-team→#ops-pool routing (Option A).
type MessageRouter struct {
	config  *client.Config
	relay   *client.RelayClient
	sse     *client.SSEClient
	session *discordgo.Session

	// threadMap: conversation_id → Discord thread ID
	threadMap   map[string]string
	threadMapMu sync.RWMutex

	stopCh chan struct{}
}

// NewMessageRouter creates a router wired to relay SSE.
func NewMessageRouter(cfg *client.Config, relay *client.RelayClient, sse *client.SSEClient) *MessageRouter {
	return &MessageRouter{
		config:    cfg,
		relay:     relay,
		sse:       sse,
		threadMap: make(map[string]string),
		stopCh:    make(chan struct{}),
	}
}

// Start registers SSE handlers and begins routing.
func (r *MessageRouter) Start(s *discordgo.Session) {
	r.session = s

	// Register for message events from relay
	r.sse.On("message", r.onRelayMessage)
	r.sse.On("conversation", r.onRelayConversation)

	log.Printf("[discord:router] started")
}

// Stop signals the router to shut down.
func (r *MessageRouter) Stop() {
	close(r.stopCh)
	log.Printf("[discord:router] stopped")
}

// onRelayMessage routes a relay message to the correct Discord channel/thread.
func (r *MessageRouter) onRelayMessage(evt client.SSEEvent) {
	if r.session == nil {
		return
	}

	from, _ := evt.Data["from"].(string)
	to, _ := evt.Data["to"].(string)
	subject, _ := evt.Data["subject"].(string)
	content, _ := evt.Data["content"].(string)
	msgType, _ := evt.Data["type"].(string)
	msgID, _ := evt.Data["id"].(string)
	convID, _ := evt.Data["conversation_id"].(string)

	// Only route messages FROM agents TO humans (or broadcasts)
	if !r.isAgent(from) {
		return
	}
	if to != "*" && to != "user" && !r.isHuman(to) {
		// Agent-to-agent — Discord doesn't need to see these
		return
	}

	// Build Discord message
	text := r.formatMessage(from, to, subject, content, msgType)

	// Determine destination channel
	channelID := r.resolveChannel(from, to, convID)
	if channelID == "" {
		log.Printf("[discord:router] no channel for %s→%s (conv=%s)", from, to, convID)
		return
	}

	// If this is part of a conversation, route to thread
	if convID != "" {
		threadID := r.getOrCreateThread(channelID, convID, subject)
		if threadID != "" {
			channelID = threadID
		}
	}

	// Send to Discord
	_, err := r.session.ChannelMessageSend(channelID, text)
	if err != nil {
		log.Printf("[discord:router] send failed: %v", err)
		return
	}

	// Acknowledge delivery (V0.3)
	if msgID != "" {
		go r.relay.AckDelivery("discord-bridge", msgID)
	}

	log.Printf("[discord:router] routed: %s→discord (%s)", from, channelID)
}

// onRelayConversation handles conversation lifecycle events.
func (r *MessageRouter) onRelayConversation(evt client.SSEEvent) {
	if r.session == nil {
		return
	}

	action := evt.Action
	if action != "create" {
		return
	}

	convID, _ := evt.Data["conversation_id"].(string)
	title, _ := evt.Data["title"].(string)
	membersRaw, _ := evt.Data["members"].([]interface{})

	if convID == "" || title == "" {
		return
	}

	members := make([]string, 0, len(membersRaw))
	for _, m := range membersRaw {
		if s, ok := m.(string); ok {
			members = append(members, s)
		}
	}

	// Determine which channel to create the thread in
	channelID := r.resolveConversationChannel(members)
	if channelID == "" {
		return
	}

	// Create a thread for this conversation
	threadID := r.createThread(channelID, convID, title)
	if threadID != "" {
		log.Printf("[discord:router] created thread for conversation %s: %s", convID, threadID)
	}
}

// resolveChannel determines which Discord channel a message should go to.
// Implements Option A: cross-team conversations → #ops-pool.
func (r *MessageRouter) resolveChannel(from, to, convID string) string {
	// If targeting a specific human, use their pool's channel
	if r.isHuman(to) {
		return r.humanChannel(to)
	}

	// Broadcast or "user" target — use the sender's pool channel
	fromPool := r.agentPool(from)
	if fromPool != "" {
		if ch, ok := r.config.Discord.Channels[fromPool]; ok {
			return ch
		}
	}

	// Fallback: first channel in config
	for _, ch := range r.config.Discord.Channels {
		return ch
	}
	return ""
}

// resolveConversationChannel determines which channel a conversation thread belongs to.
// If all members share a single pool → that pool's channel.
// If members span multiple pools → #ops-pool channel (Option A).
func (r *MessageRouter) resolveConversationChannel(members []string) string {
	pools := make(map[string]bool)
	for _, member := range members {
		pool := r.agentPool(member)
		if pool != "" {
			pools[pool] = true
		}
		// Humans can be in multiple pools, count all
		if human, ok := r.config.Humans[member]; ok {
			for _, p := range human.Pools {
				pools[p] = true
			}
		}
	}

	if len(pools) == 1 {
		// Single pool — use that pool's channel
		for pool := range pools {
			if ch, ok := r.config.Discord.Channels[pool]; ok {
				return ch
			}
		}
	}

	// Cross-team: route to ops pool channel (Option A)
	if ch, ok := r.config.Discord.Channels["ops"]; ok {
		return ch
	}

	// Fallback: first channel
	for _, ch := range r.config.Discord.Channels {
		return ch
	}
	return ""
}

// getOrCreateThread returns the Discord thread for a conversation, creating if needed.
func (r *MessageRouter) getOrCreateThread(channelID, convID, subject string) string {
	r.threadMapMu.RLock()
	threadID, ok := r.threadMap[convID]
	r.threadMapMu.RUnlock()
	if ok {
		return threadID
	}

	return r.createThread(channelID, convID, subject)
}

// createThread creates a Discord thread and caches the mapping.
func (r *MessageRouter) createThread(channelID, convID, title string) string {
	if title == "" {
		title = "Conversation"
	}
	// Discord thread name limit: 100 chars
	if len(title) > 95 {
		title = title[:92] + "..."
	}

	// Create a starter message for the thread
	starterMsg, err := r.session.ChannelMessageSend(channelID, fmt.Sprintf("🧵 **%s**", title))
	if err != nil {
		log.Printf("[discord:router] create thread starter failed: %v", err)
		return ""
	}

	thread, err := r.session.MessageThreadStartComplex(channelID, starterMsg.ID, &discordgo.ThreadStart{
		Name:                title,
		AutoArchiveDuration: 1440, // 24 hours
		Type:                discordgo.ChannelTypeGuildPublicThread,
	})
	if err != nil {
		log.Printf("[discord:router] create thread failed: %v", err)
		return ""
	}

	r.threadMapMu.Lock()
	r.threadMap[convID] = thread.ID
	r.threadMapMu.Unlock()

	return thread.ID
}

// formatMessage builds a Discord message from relay message fields.
func (r *MessageRouter) formatMessage(from, to, subject, content, msgType string) string {
	var b strings.Builder

	// Header
	emoji := "💬"
	switch msgType {
	case "question":
		emoji = "❓"
	case "notification":
		emoji = "📢"
	case "user_question":
		emoji = "🙋"
	case "status_update":
		emoji = "📊"
	}

	fmt.Fprintf(&b, "%s **%s**", emoji, from)
	if to != "*" && to != "user" {
		fmt.Fprintf(&b, " → **%s**", to)
	}
	if subject != "" {
		fmt.Fprintf(&b, " | %s", subject)
	}
	b.WriteString("\n")

	// Body
	if content != "" {
		// Wrap long content in a code block for readability
		if len(content) > 500 {
			fmt.Fprintf(&b, "```\n%s\n```", content)
		} else {
			b.WriteString(content)
		}
	}

	return b.String()
}

// --- Lookup helpers ---

func (r *MessageRouter) isAgent(name string) bool {
	_, ok := r.config.Agents[name]
	return ok
}

func (r *MessageRouter) isHuman(name string) bool {
	_, ok := r.config.Humans[name]
	return ok
}

func (r *MessageRouter) agentPool(agentName string) string {
	if agent, ok := r.config.Agents[agentName]; ok {
		return agent.Pool
	}
	return ""
}

func (r *MessageRouter) humanChannel(humanSlug string) string {
	human, ok := r.config.Humans[humanSlug]
	if !ok || len(human.Pools) == 0 {
		return ""
	}

	// Use the human's first pool's channel
	if ch, ok := r.config.Discord.Channels[human.Pools[0]]; ok {
		return ch
	}
	return ""
}

// ListConversations returns the current thread map (for debugging).
func (r *MessageRouter) ListConversations() map[string]string {
	r.threadMapMu.RLock()
	defer r.threadMapMu.RUnlock()

	out := make(map[string]string, len(r.threadMap))
	for k, v := range r.threadMap {
		out[k] = v
	}
	return out
}

// InjectThreadMapping allows pre-seeding the thread map (e.g., from persistent storage).
func (r *MessageRouter) InjectThreadMapping(convID, threadID string) {
	r.threadMapMu.Lock()
	r.threadMap[convID] = threadID
	r.threadMapMu.Unlock()
}

// extractConversationMembers extracts member list from relay conversation data.
func extractConversationMembers(data json.RawMessage) []string {
	var conv struct {
		Members []string `json:"members"`
	}
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil
	}
	return conv.Members
}
