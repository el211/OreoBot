package bot

import (
	"log"

	"discord-bot/config"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	Session *discordgo.Session
	Config  *config.Config
}

func New(cfg *config.Config) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsAll
	return &Bot{Session: s, Config: cfg}, nil
}

func (b *Bot) Start() error {
	b.Session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Bot is online as %s#%s", r.User.Username, r.User.Discriminator)
	})
	return b.Session.Open()
}

func (b *Bot) Stop() {
	_ = b.Session.Close()
}

func (b *Bot) RegisterCommands(cmds []*discordgo.ApplicationCommand) []*discordgo.ApplicationCommand {
	registered := make([]*discordgo.ApplicationCommand, 0, len(cmds))
	for _, cmd := range cmds {
		rc, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, b.Config.Discord.GuildID, cmd)
		if err != nil {
			log.Printf("Cannot register command %q: %v", cmd.Name, err)
			continue
		}
		registered = append(registered, rc)
	}
	log.Printf("Registered %d slash commands", len(registered))
	return registered
}

func (b *Bot) CleanupCommands(cmds []*discordgo.ApplicationCommand) {
	for _, cmd := range cmds {
		if err := b.Session.ApplicationCommandDelete(b.Session.State.User.ID, b.Config.Discord.GuildID, cmd.ID); err != nil {
			log.Printf("Cannot delete command %q: %v", cmd.Name, err)
		}
	}
	log.Println("Cleaned up slash commands")
}
