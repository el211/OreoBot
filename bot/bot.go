package bot

import (
	"log"

	"discord-bot/config"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	Session *discordgo.Session
	Config  *config.Config
	ready   chan struct{}
}

func New(cfg *config.Config) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsAll
	return &Bot{
		Session: s,
		Config:  cfg,
		ready:   make(chan struct{}),
	}, nil
}

func (b *Bot) Start() error {
	b.Session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Bot is online as %s#%s", r.User.Username, r.User.Discriminator)
		select {
		case <-b.ready:
		default:
			close(b.ready)
		}
	})
	return b.Session.Open()
}

func (b *Bot) Stop() {
	_ = b.Session.Close()
}

func (b *Bot) RegisterCommands(cmds []*discordgo.ApplicationCommand) []*discordgo.ApplicationCommand {
	<-b.ready

	appID := b.Session.State.User.ID
	guildID := b.Config.Discord.GuildID

	log.Printf("Registering %d commands for app %s in guild %s", len(cmds), appID, guildID)

	registered, err := b.Session.ApplicationCommandBulkOverwrite(appID, guildID, cmds)
	if err != nil {
		log.Printf("Failed to bulk-overwrite commands: %v", err)
		return nil
	}

	log.Printf("Successfully registered %d slash commands", len(registered))
	return registered
}

func (b *Bot) CleanupCommands(_ []*discordgo.ApplicationCommand) {
	<-b.ready
	appID := b.Session.State.User.ID
	guildID := b.Config.Discord.GuildID
	if _, err := b.Session.ApplicationCommandBulkOverwrite(appID, guildID, []*discordgo.ApplicationCommand{}); err != nil {
		log.Printf("Failed to clean up commands: %v", err)
		return
	}
	log.Println("Cleaned up all slash commands")
}
