package handlers

import (
	"fmt"
	"strings"

	"discord-bot/minecraft"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

var RCONClient *minecraft.Client

func minecraftCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "mc",
			Description:              "Minecraft server management",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name: "status", Description: "Check if the Minecraft server is reachable",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name: "command", Description: "Execute an RCON command on the Minecraft server",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "cmd", Description: "The command to run (e.g. list, whitelist add Steve)", Required: true},
					},
				},
				{
					Name: "players", Description: "List online players",
					Type: discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name: "say", Description: "Broadcast a message in-game",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "message", Description: "Message to broadcast", Required: true},
					},
				},
				{
					Name: "whitelist", Description: "Add or remove a player from the whitelist",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "action", Description: "add / remove", Required: true,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "add", Value: "add"},
								{Name: "remove", Value: "remove"},
							}},
						{Type: discordgo.ApplicationCommandOptionString, Name: "player", Description: "Player name", Required: true},
					},
				},
			},
		},
	}
}

func handleMinecraftCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cfg := storage.Cfg
	if !cfg.Minecraft.Enabled {
		respond(s, i, "Minecraft integration is disabled in config.json.", true)
		return
	}
	if RCONClient == nil {
		respond(s, i, "RCON client not initialised.", true)
		return

	}
	sub := i.ApplicationCommandData().Options[0]
	switch sub.Name {
	case "status":
		handleMCStatus(s, i)
	case "command":
		handleMCCommand(s, i, sub.Options)
	case "players":
		handleMCPlayers(s, i)
	case "say":
		handleMCSay(s, i, sub.Options)
	case "whitelist":
		handleMCWhitelist(s, i, sub.Options)
	}
}

func handleMCStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !RCONClient.IsConnected() {
		if err := RCONClient.Connect(); err != nil {
			respond(s, i, fmt.Sprintf("Server unreachable: %v", err), true)
			return
		}
	}
	respond(s, i, "Minecraft server is **online** and RCON is connected.", true)
}

func handleMCCommand(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	if !isAdmin(s, i) {
		respond(s, i, "Only admins can run raw RCON commands.", true)
		return
	}

	om := subOptMap(opts)
	cmd := om["cmd"].StringValue()

	resp, err := RCONClient.Command(cmd)
	if err != nil {
		respond(s, i, fmt.Sprintf("RCON error: %v", err), true)
		return
	}

	if resp == "" {
		resp = "(no output)"
	}
	if len(resp) > 1900 {
		resp = resp[:1900] + "..."
	}

	respond(s, i, fmt.Sprintf("```\n> %s\n%s\n```", cmd, resp), true)
}

func handleMCPlayers(s *discordgo.Session, i *discordgo.InteractionCreate) {
	resp, err := RCONClient.Command("list")
	if err != nil {
		respond(s, i, fmt.Sprintf("RCON error: %v", err), true)
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "⛏️ Online Players",
		Description: resp,
		Color:       0x55FF55,
	}
	respondEmbed(s, i, embed, true)
}

func handleMCSay(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	message := om["message"].StringValue()

	_, err := RCONClient.Command(fmt.Sprintf("say [Discord] %s: %s", i.Member.User.Username, message))
	if err != nil {
		respond(s, i, fmt.Sprintf("RCON error: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("Sent to server: **%s**", message), false)
}

func handleMCWhitelist(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	action := om["action"].StringValue()
	player := om["player"].StringValue()

	player = strings.ReplaceAll(player, " ", "")
	if len(player) > 16 {
		respond(s, i, "Invalid player name.", true)
		return
	}

	resp, err := RCONClient.Command(fmt.Sprintf("whitelist %s %s", action, player))
	if err != nil {
		respond(s, i, fmt.Sprintf("RCON error: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("`whitelist %s %s` → %s", action, player, resp), true)
}
