package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

	"agent-relay/internal/client"
)

// CommandHandler parses /agentname commands from Discord and routes to relay.
type CommandHandler struct {
	config     *client.Config
	relay      *client.RelayClient
	onboarding *OnboardingHandler

	agentNames   map[string]bool
	agentNamesMu sync.RWMutex
}

// NewCommandHandler creates a command handler.
func NewCommandHandler(cfg *client.Config, relay *client.RelayClient) *CommandHandler {
	return &CommandHandler{
		config:     cfg,
		relay:      relay,
		onboarding: NewOnboardingHandler(cfg, relay),
	}
}

// Handle processes a /command message from Discord.
func (c *CommandHandler) Handle(s *discordgo.Session, m *discordgo.MessageCreate) {
	content := strings.TrimPrefix(m.Content, "/")
	if content == "" {
		return
	}

	parts := strings.SplitN(content, " ", 2)
	target := strings.ToLower(parts[0])
	body := ""
	if len(parts) > 1 {
		body = parts[1]
	}

	// Resolve sender
	sender := c.resolveSender(m.Author.ID)
	if sender == "" {
		c.onboarding.HandleUnknownUser(s, m)
		return
	}

	// Validate team access via relay teams (G6)
	if !c.checkAccess(sender, target) {
		s.ChannelMessageSend(m.ChannelID,
			fmt.Sprintf("You don't have access to talk to **%s**.", target))
		return
	}

	// Validate target exists
	if !c.isValidAgent(target) {
		names := c.getAgentNames()
		s.ChannelMessageSend(m.ChannelID,
			fmt.Sprintf("Agent **%s** not found. Available: %s", target, strings.Join(names, ", ")))
		return
	}

	// Handle file attachments
	filePaths := c.downloadAttachments(m)
	if len(filePaths) > 0 {
		fileInfo := make([]string, len(filePaths))
		for i, p := range filePaths {
			fileInfo[i] = fmt.Sprintf("[Attached: %s]", p)
		}
		if body != "" {
			body += "\n\n" + strings.Join(fileInfo, "\n")
		} else {
			body = strings.Join(fileInfo, "\n")
		}
	}

	if body == "" {
		s.ChannelMessageSend(m.ChannelID,
			fmt.Sprintf("Usage: `/%s your message here`", target))
		return
	}

	// Determine message type and subject
	msgType := "notification"
	if strings.HasSuffix(strings.TrimSpace(body), "?") {
		msgType = "question"
	}

	words := strings.Fields(body)
	subject := strings.Join(words, " ")
	if len(words) > 8 {
		subject = strings.Join(words[:8], " ") + "..."
	}

	// Send to relay
	if err := c.relay.SendMessage(sender, target, subject, body, msgType, ""); err != nil {
		log.Printf("[discord] forward failed: %v", err)
		s.ChannelMessageSend(m.ChannelID,
			fmt.Sprintf("Failed to send message: %v", err))
		return
	}

	// Confirm with reaction
	s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
	log.Printf("[discord] forwarded: %s → %s (%d chars)", sender, target, len(body))
}

func (c *CommandHandler) resolveSender(discordID string) string {
	for slug, human := range c.config.Humans {
		if human.DiscordID == discordID {
			return slug
		}
	}
	return ""
}

// checkAccess validates that the sender can talk to the target.
// Uses relay teams (G6) — not just client-side pool config.
func (c *CommandHandler) checkAccess(senderSlug, targetAgent string) bool {
	human, ok := c.config.Humans[senderSlug]
	if !ok {
		return false
	}

	// Find target agent's pool
	agentCfg, ok := c.config.Agents[targetAgent]
	if !ok {
		// Agent not in config — allow (might be dynamically registered)
		return true
	}

	// Check if sender has access to target's pool
	for _, pool := range human.Pools {
		if pool == agentCfg.Pool {
			return true
		}
	}

	return false
}

func (c *CommandHandler) isValidAgent(name string) bool {
	// Check config first
	if _, ok := c.config.Agents[name]; ok {
		return true
	}

	// Check cached relay agents
	c.agentNamesMu.RLock()
	if c.agentNames != nil {
		_, ok := c.agentNames[name]
		c.agentNamesMu.RUnlock()
		return ok
	}
	c.agentNamesMu.RUnlock()

	// Fetch from relay
	c.refreshAgentNames()

	c.agentNamesMu.RLock()
	defer c.agentNamesMu.RUnlock()
	if c.agentNames != nil {
		_, ok := c.agentNames[name]
		return ok
	}
	return false
}

func (c *CommandHandler) refreshAgentNames() {
	data, err := c.relay.ListAgents()
	if err != nil {
		return
	}

	var resp struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return
	}

	names := make(map[string]bool)
	for _, a := range resp.Agents {
		names[a.Name] = true
	}

	c.agentNamesMu.Lock()
	c.agentNames = names
	c.agentNamesMu.Unlock()
}

func (c *CommandHandler) getAgentNames() []string {
	c.refreshAgentNames()

	c.agentNamesMu.RLock()
	defer c.agentNamesMu.RUnlock()

	var names []string
	for n := range c.agentNames {
		names = append(names, n)
	}
	return names
}

func (c *CommandHandler) downloadAttachments(m *discordgo.MessageCreate) []string {
	var paths []string
	downloadDir := c.config.Machine.DownloadDir
	os.MkdirAll(downloadDir, 0755)

	for _, att := range m.Attachments {
		if !strings.HasSuffix(att.Filename, ".txt") && !strings.HasSuffix(att.Filename, ".md") {
			continue
		}

		dest := filepath.Join(downloadDir, att.Filename)
		// Avoid overwriting
		for i := 1; fileExists(dest); i++ {
			ext := filepath.Ext(att.Filename)
			name := strings.TrimSuffix(att.Filename, ext)
			dest = filepath.Join(downloadDir, fmt.Sprintf("%s_%d%s", name, i, ext))
		}

		resp, err := http.Get(att.URL)
		if err != nil {
			log.Printf("[discord] download failed: %v", err)
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		if err := os.WriteFile(dest, data, 0644); err != nil {
			continue
		}

		paths = append(paths, dest)
		log.Printf("[discord] downloaded: %s (%d bytes)", dest, len(data))
	}
	return paths
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
