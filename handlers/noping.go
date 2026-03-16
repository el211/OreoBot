package handlers

import (
	"log"
	"strings"

	"discord-bot/config"

	"github.com/bwmarrin/discordgo"
)

func RegisterNoPing(s *discordgo.Session, cfg *config.Config) {
	if !cfg.NoPing.Enabled || len(cfg.NoPing.ProtectedRoles) == 0 {
		return
	}

	// Build a fast lookup set from the protected role IDs.
	protected := make(map[string]bool, len(cfg.NoPing.ProtectedRoles))
	for _, id := range cfg.NoPing.ProtectedRoles {
		protected[strings.TrimSpace(id)] = true
	}

	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		handleNoPing(s, m, &cfg.NoPing, protected)
	})

	log.Printf("[NoPing] Active — protecting %d role(s)", len(protected))
}

func handleNoPing(s *discordgo.Session, m *discordgo.MessageCreate, cfg *config.NoPingConfig, protected map[string]bool) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	// Check direct role pings (@Owner, @Admin, etc.)
	for _, roleID := range m.MentionRoles {
		if !protected[roleID] {
			continue
		}
		roleName := roleID
		if r, err := s.State.Role(m.GuildID, roleID); err == nil {
			roleName = r.Name
		}
		triggerNoPing(s, m, cfg, roleName)
		return
	}

	// Check user pings — if the pinged user holds a protected role, block it too.
	// e.g. @saladedecparisun where saladedecparisun has the Owner role.
	for _, mentioned := range m.Mentions {
		if mentioned.ID == m.Author.ID {
			continue // ignore self-mentions
		}
		member, err := s.GuildMember(m.GuildID, mentioned.ID)
		if err != nil {
			continue
		}
		for _, roleID := range member.Roles {
			if !protected[roleID] {
				continue
			}
			roleName := roleID
			if r, err := s.State.Role(m.GuildID, roleID); err == nil {
				roleName = r.Name
			}
			triggerNoPing(s, m, cfg, roleName)
			return
		}
	}
}

func triggerNoPing(s *discordgo.Session, m *discordgo.MessageCreate, cfg *config.NoPingConfig, roleName string) {
	if cfg.DeleteMessage {
		_ = s.ChannelMessageDelete(m.ChannelID, m.ID)
	}
	msg := buildNoPingMessage(cfg.Message, m.Author.ID, roleName)
	sendTemp(s, m.ChannelID, msg, 8)
}

// buildNoPingMessage replaces {user} and {role} placeholders in the configured message.
// Falls back to a sensible default if the message is empty.
func buildNoPingMessage(template, userID, roleName string) string {
	if template == "" {
		template = "{user} You are not allowed to ping **{role}**!"
	}
	msg := strings.ReplaceAll(template, "{user}", "<@"+userID+">")
	msg = strings.ReplaceAll(msg, "{role}", roleName)
	return msg
}
