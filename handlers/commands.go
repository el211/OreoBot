package handlers

import (
	"log"
	"strconv"
	"strings"

	"discord-bot/config"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

func Commands(cfg *config.Config) []*discordgo.ApplicationCommand {
	cmds := make([]*discordgo.ApplicationCommand, 0)
	cmds = append(cmds, moderationCommands()...)
	cmds = append(cmds, ticketCommands()...)
	cmds = append(cmds, utilityCommands()...)
	cmds = append(cmds, autoroleCommands()...)
	cmds = append(cmds, giveawayCommands()...)
	if cfg.Minecraft.Enabled {
		cmds = append(cmds, minecraftCommands()...)
	}
	if cfg.Music.Enabled {
		cmds = append(cmds, musicCommands()...)
	}
	cmds = append(cmds, buildCustomCommands(cfg)...)
	return cmds
}

func buildCustomCommands(cfg *config.Config) []*discordgo.ApplicationCommand {
	cmds := make([]*discordgo.ApplicationCommand, 0, len(cfg.CustomCommands))
	for _, cc := range cfg.CustomCommands {
		if cc.Name == "" || cc.Message == "" {
			log.Printf("[CustomCmd] Skipping entry with empty name or message")
			continue
		}
		desc := cc.Description
		if desc == "" {
			desc = cc.Name
		}
		cmds = append(cmds, &discordgo.ApplicationCommand{
			Name:        strings.ToLower(cc.Name),
			Description: desc,
		})
	}
	return cmds
}

// customCommandMap holds the registered custom commands for fast lookup at runtime.
var customCommandMap map[string]config.CustomCommandConfig

func Register(s *discordgo.Session) {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID == "" {
			return
		}

		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			handleSlashCommand(s, i)
		case discordgo.InteractionMessageComponent:
			handleComponent(s, i)
		}
	})
}

// RegisterCustomCommands builds the runtime lookup map from config.
// Must be called before the bot starts processing interactions.
func RegisterCustomCommands(cfg *config.Config) {
	customCommandMap = make(map[string]config.CustomCommandConfig, len(cfg.CustomCommands))
	for _, cc := range cfg.CustomCommands {
		if cc.Name != "" && cc.Message != "" {
			customCommandMap[strings.ToLower(cc.Name)] = cc
		}
	}
	log.Printf("[CustomCmd] Registered %d custom command(s)", len(customCommandMap))
}

func handleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	name := i.ApplicationCommandData().Name

	switch name {
	case "ban":
		handleBan(s, i)
	case "unban":
		handleUnban(s, i)
	case "kick":
		handleKick(s, i)
	case "mute":
		handleMute(s, i)
	case "unmute":
		handleUnmute(s, i)
	case "warn":
		handleWarn(s, i)
	case "warnings":
		handleWarnings(s, i)
	case "clearwarnings":
		handleClearWarnings(s, i)
	case "purge", "clear":
		handlePurge(s, i)
	case "slowmode":
		handleSlowmode(s, i)
	case "lock":
		handleLock(s, i)
	case "unlock":
		handleUnlock(s, i)
	case "modlog":
		handleModlog(s, i)
	case "userinfo":
		handleUserinfo(s, i)

	case "ticket":
		handleTicketCommand(s, i)
	case "close":
		handleCloseCommand(s, i)
	case "add":
		handleAddUser(s, i)
	case "remove":
		handleRemoveUser(s, i)

	case "mc":
		handleMinecraftCommand(s, i)

	case "say":
		handleSay(s, i)
	case "embed":
		handleEmbed(s, i)

	case "play", "skip", "stop", "queue", "volume", "nowplaying", "pause", "resume":
		handleMusicCommand(s, i, name)

	case "joinrole":
		handleJoinRoleCommand(s, i)
	case "rolemenu":
		handleRoleMenuCommand(s, i)
	case "giveaway":
		handleGiveawayCommand(s, i)

	default:
		// Check if it's a user-defined custom command.
		if cc, ok := customCommandMap[name]; ok {
			respond(s, i, resolveCustomPlaceholders(s, i, cc.Message), cc.Ephemeral)
			return
		}
		log.Printf("Unknown command: %s", name)
	}
}

func handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	if strings.HasPrefix(customID, "rolemenu:") {
		HandleRoleMenuButton(s, i)
		return
	}
	if strings.HasPrefix(customID, "giveaway_enter:") {
		HandleGiveawayEnter(s, i)
		return
	}
	if strings.HasPrefix(customID, "giveaway_ended_") {
		return
	}

	switch customID {
	case "ticket_category_select":
		handleTicketCategorySelect(s, i)
	case "ticket_subcategory_select":
		handleTicketSubcategorySelect(s, i)
	case "ticket_close_btn":
		handleCloseButton(s, i)
	case "ticket_close_confirm":
		handleCloseConfirm(s, i)
	case "ticket_close_cancel":
		handleCloseCancel(s, i)
	default:
		log.Printf("Unknown component: %s", customID)
	}
}

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   flags,
		},
	})
	if err != nil {
		log.Printf("Failed to respond: %v", err)
	}
}

func respondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  flags,
		},
	})
}

func followup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
}

func optionMap(i *discordgo.InteractionCreate) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*discordgo.ApplicationCommandInteractionDataOption)
	for _, opt := range i.ApplicationCommandData().Options {
		m[opt.Name] = opt
	}
	return m
}

func subOptMap(opts []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*discordgo.ApplicationCommandInteractionDataOption)
	for _, opt := range opts {
		m[opt.Name] = opt
	}
	return m
}

func optStr(m map[string]*discordgo.ApplicationCommandInteractionDataOption, key, def string) string {
	if o, ok := m[key]; ok {
		return o.StringValue()
	}
	return def
}

func optInt(m map[string]*discordgo.ApplicationCommandInteractionDataOption, key string, def int64) int64 {
	if o, ok := m[key]; ok {
		return o.IntValue()
	}
	return def
}

// resolveCustomPlaceholders replaces Discord-native placeholders in a custom command message.
//
//	{user}         → mentions the user who ran the command
//	{username}     → their Discord username
//	{server}       → guild name
//	{member_count} → number of members in the guild
func resolveCustomPlaceholders(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) string {
	if i.Member == nil || i.Member.User == nil {
		return msg
	}

	msg = strings.ReplaceAll(msg, "{user}", "<@"+i.Member.User.ID+">")
	msg = strings.ReplaceAll(msg, "{username}", i.Member.User.Username)

	if guild, err := s.Guild(i.GuildID); err == nil {
		msg = strings.ReplaceAll(msg, "{server}", guild.Name)
		msg = strings.ReplaceAll(msg, "{member_count}", strconv.Itoa(guild.MemberCount))
	}

	return msg
}

func hasConfigRole(s *discordgo.Session, guildID string, member *discordgo.Member, allowedNames []string) bool {
	if member == nil || len(allowedNames) == 0 {
		return false
	}

	roles, err := s.GuildRoles(guildID)
	if err != nil {
		return false
	}

	nameSet := make(map[string]bool, len(allowedNames))
	for _, n := range allowedNames {
		nameSet[strings.ToLower(n)] = true
	}

	for _, role := range roles {
		if nameSet[strings.ToLower(role.Name)] {
			for _, memberRoleID := range member.Roles {
				if memberRoleID == role.ID {
					return true
				}
			}
		}
	}
	return false
}

func isAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	cfg := storage.Cfg
	if i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true
	}
	return hasConfigRole(s, i.GuildID, i.Member, cfg.Permissions.AdminRoles)
}

func isModerator(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if isAdmin(s, i) {
		return true
	}
	cfg := storage.Cfg
	if i.Member.Permissions&discordgo.PermissionBanMembers != 0 {
		return true
	}
	return hasConfigRole(s, i.GuildID, i.Member, cfg.Permissions.ModeratorRoles)
}

func isDJ(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if isModerator(s, i) {
		return true
	}
	cfg := storage.Cfg
	return hasConfigRole(s, i.GuildID, i.Member, cfg.Permissions.DJRoles)
}
