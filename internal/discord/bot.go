// Package discord implements the WRAI.TH Discord bridge.
// Pure message forwarder — zero LLM tokens. Station mode only.
package discord

import (
	"log"

	"github.com/bwmarrin/discordgo"

	"agent-relay/internal/client"
)

// Bot wraps the Discord session and wires commands + routing.
type Bot struct {
	session  *discordgo.Session
	config   *client.Config
	relay    *client.RelayClient
	commands *CommandHandler
	router   *MessageRouter
}

// NewBot creates a Discord bot from config.
func NewBot(cfg *client.Config, relay *client.RelayClient, sse *client.SSEClient) (*Bot, error) {
	dg, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, err
	}

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	b := &Bot{
		session:  dg,
		config:   cfg,
		relay:    relay,
		commands: NewCommandHandler(cfg, relay),
		router:   NewMessageRouter(cfg, relay, sse),
	}

	dg.AddHandler(b.onMessageCreate)
	dg.AddHandler(b.onReady)

	return b, nil
}

// Start connects to Discord and begins routing.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return err
	}

	// Start outbound routing (SSE-driven, not polling)
	b.router.Start(b.session)

	log.Printf("[discord] connected as %s", b.session.State.User.Username)
	return nil
}

// Stop disconnects from Discord.
func (b *Bot) Stop() {
	b.router.Stop()
	b.session.Close()
	log.Printf("[discord] disconnected")
}

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Printf("[discord] ready: %s#%s (guilds: %d)", r.User.Username, r.User.Discriminator, len(r.Guilds))
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Ignore messages not starting with /
	if len(m.Content) == 0 || m.Content[0] != '/' {
		return
	}

	b.commands.Handle(s, m)
}
