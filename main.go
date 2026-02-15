package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"discord-bot/bot"
	"discord-bot/config"
	"discord-bot/handlers"
	"discord-bot/minecraft"
	"discord-bot/music"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to config file")
	cleanup := flag.Bool("cleanup", false, "Remove slash commands on shutdown")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if cfg.Discord.Token == "" || cfg.Discord.Token == "YOUR_DISCORD_BOT_TOKEN_HERE" {
		log.Fatal("Set your bot token in config.json → discord.token")
	}

	storage.Cfg = cfg

	if err := storage.InitDB(&cfg.Database); err != nil {
		log.Printf("WARNING: Database init failed (%v). Falling back to JSON-only storage.", err)
	} else {
		defer storage.DB.Close()
	}

	if cfg.Minecraft.Enabled {
		rcon := minecraft.NewClient(cfg.Minecraft.RCONAddress, cfg.Minecraft.RCONPort, cfg.Minecraft.RCONPassword)
		if err := rcon.Connect(); err != nil {
			log.Printf("WARNING: Minecraft RCON connection failed: %v (commands will retry on use)", err)
		} else {
			log.Println("Minecraft RCON connected")
		}
		handlers.RCONClient = rcon
	}

	b, err := bot.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	handlers.Register(b.Session)
	handlers.RegisterWelcomeLeave(b.Session)
	b.Session.AddHandler(func(s *discordgo.Session, e *discordgo.VoiceStateUpdate) {
		if s.State != nil && s.State.User != nil && e.UserID == s.State.User.ID {
			music.UpdateVoiceState(e.GuildID, e.SessionID)
		}
	})

	b.Session.AddHandler(func(s *discordgo.Session, e *discordgo.VoiceServerUpdate) {
		music.UpdateVoiceServer(e.GuildID, e.Token, e.Endpoint)
	})

	if err := b.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}
	defer b.Stop()

	if cfg.Music.Enabled {
		mgr, err := music.NewManager(b.Session, &cfg.Music)
		if err != nil {
			log.Printf("WARNING: Music system init failed: %v", err)
			log.Println("Music commands will show an error. Fix the issue and restart.")
		} else {
			handlers.MusicMgr = mgr
			defer mgr.Cleanup()
		}
	}

	registered := b.RegisterCommands(handlers.Commands(cfg))

	log.Println("Bot is running. Press Ctrl+C to exit.")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down...")
	if *cleanup {
		b.CleanupCommands(registered)
	}
}
