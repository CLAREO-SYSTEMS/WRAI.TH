package discord

import (
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"

	"agent-relay/internal/client"
)

// OnboardingHandler deals with unknown Discord users trying to use the bot.
// Notifies the CTO (or first executive) so they can add the user to config.
type OnboardingHandler struct {
	config *client.Config
	relay  *client.RelayClient
}

// NewOnboardingHandler creates an onboarding handler.
func NewOnboardingHandler(cfg *client.Config, relay *client.RelayClient) *OnboardingHandler {
	return &OnboardingHandler{
		config: cfg,
		relay:  relay,
	}
}

// HandleUnknownUser responds to unrecognized Discord users and alerts an executive.
func (o *OnboardingHandler) HandleUnknownUser(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Reply to the user
	s.ChannelMessageSend(m.ChannelID,
		fmt.Sprintf("I don't recognize your Discord account (`%s#%s`, ID: %s). "+
			"Ask an administrator to add you to the relay config.",
			m.Author.Username, m.Author.Discriminator, m.Author.ID))

	// Notify executive via relay
	exec := o.findExecutive()
	if exec == "" {
		log.Printf("[discord:onboarding] unknown user %s (%s) — no executive to notify",
			m.Author.Username, m.Author.ID)
		return
	}

	subject := fmt.Sprintf("Unknown Discord user: %s", m.Author.Username)
	content := fmt.Sprintf(
		"Discord user **%s#%s** (ID: `%s`) tried to use the bot in channel `%s`.\n\n"+
			"To add them, update the `humans` section in the relay config with their discord_id.",
		m.Author.Username, m.Author.Discriminator, m.Author.ID, m.ChannelID)

	if err := o.relay.SendMessage("discord-bridge", exec, subject, content, "notification", "P2"); err != nil {
		log.Printf("[discord:onboarding] failed to notify %s: %v", exec, err)
	} else {
		log.Printf("[discord:onboarding] notified %s about unknown user %s", exec, m.Author.Username)
	}
}

// findExecutive returns the slug of the first executive human in config.
// Falls back to any human if no executive is marked.
func (o *OnboardingHandler) findExecutive() string {
	// First pass: look for is_executive: true
	for slug, human := range o.config.Humans {
		if human.IsExecutive {
			return slug
		}
	}

	// Second pass: any human (better than nothing)
	for slug := range o.config.Humans {
		return slug
	}

	return ""
}
