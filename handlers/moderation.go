package handlers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"discord-bot/config"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

var modPermission int64 = discordgo.PermissionBanMembers
var adminPerm int64 = discordgo.PermissionAdministrator

func moderationCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "ban",
			Description:              "Ban a member from the server",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to ban", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Reason for ban"},
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "days", Description: "Days of messages to delete (0-7)"},
			},
		},
		{
			Name:                     "unban",
			Description:              "Unban a user from the server",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "user-id", Description: "User ID to unban", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Reason for unban"},
			},
		},
		{
			Name:                     "kick",
			Description:              "Kick a member from the server",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to kick", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Reason for kick"},
			},
		},
		{
			Name:                     "mute",
			Description:              "Timeout (mute) a member",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to mute", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "duration", Description: "Duration (e.g. 10m, 1h, 1d)", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Reason for mute"},
			},
		},
		{
			Name:                     "unmute",
			Description:              "Remove timeout from a member",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to unmute", Required: true},
			},
		},
		{
			Name:                     "warn",
			Description:              "Issue a warning to a member",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to warn", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Reason for warning", Required: true},
			},
		},
		{
			Name:                     "warnings",
			Description:              "View warnings for a member",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to check", Required: true},
			},
		},
		{
			Name:                     "clearwarnings",
			Description:              "Clear all warnings for a member",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to clear warnings for", Required: true},
			},
		},
		{
			Name:                     "purge",
			Description:              "Delete a number of messages from the channel",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "count", Description: "Number of messages to delete (1-100)", Required: true},
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Only delete messages from this user"},
			},
		},
		{
			Name:                     "slowmode",
			Description:              "Set slowmode delay for the current channel",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "seconds", Description: "Slowmode delay in seconds (0 to disable)", Required: true},
			},
		},
		{
			Name:                     "lock",
			Description:              "Lock the current channel (prevent @everyone from sending messages)",
			DefaultMemberPermissions: &modPermission,
		},
		{
			Name:                     "unlock",
			Description:              "Unlock the current channel",
			DefaultMemberPermissions: &modPermission,
		},
		{
			Name:                     "modlog",
			Description:              "Set the moderation log channel",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel for mod logs", Required: true},
			},
		},
		{
			Name:                     "userinfo",
			Description:              "Show information about a user",
			DefaultMemberPermissions: &modPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to inspect"},
			},
		},
	}
}

func handleBan(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	target := opts["user"].UserValue(s)
	reason := optStr(opts, "reason", "No reason provided")
	days := int(optInt(opts, "days", 0))
	if days > 7 {
		days = 7
	}

	err := s.GuildBanCreateWithReason(i.GuildID, target.ID, reason, days)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed to ban: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("🔨 **%s** has been banned. Reason: %s", target.Username, reason), false)
	logModAction(s, i.GuildID, "Ban", target, i.Member.User, reason, "")
}

func handleUnban(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	userID := opts["user-id"].StringValue()
	reason := optStr(opts, "reason", "No reason provided")

	err := s.GuildBanDelete(i.GuildID, userID)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed to unban: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf(" User `%s` has been unbanned. Reason: %s", userID, reason), false)
}

func handleKick(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	target := opts["user"].UserValue(s)
	reason := optStr(opts, "reason", "No reason provided")

	err := s.GuildMemberDeleteWithReason(i.GuildID, target.ID, reason)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed to kick: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("👢 **%s** has been kicked. Reason: %s", target.Username, reason), false)
	logModAction(s, i.GuildID, "Kick", target, i.Member.User, reason, "")
}

func handleMute(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	target := opts["user"].UserValue(s)
	durStr := opts["duration"].StringValue()
	reason := optStr(opts, "reason", "No reason provided")

	dur, err := parseDuration(durStr)
	if err != nil {
		respond(s, i, " Invalid duration. Use formats like `10m`, `2h`, `1d`.", true)
		return
	}
	if dur > 28*24*time.Hour {
		respond(s, i, " Maximum timeout is 28 days.", true)
		return
	}

	until := time.Now().Add(dur)
	err = s.GuildMemberTimeout(i.GuildID, target.ID, &until)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed to mute: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("🔇 **%s** has been muted for `%s`. Reason: %s", target.Username, durStr, reason), false)
	logModAction(s, i.GuildID, "Mute", target, i.Member.User, reason, durStr)
}

func handleUnmute(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	target := opts["user"].UserValue(s)

	err := s.GuildMemberTimeout(i.GuildID, target.ID, nil)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed to unmute: %v", err), true)
		return
	}

	respond(s, i, fmt.Sprintf("🔊 **%s** has been unmuted.", target.Username), false)
	logModAction(s, i.GuildID, "Unmute", target, i.Member.User, "", "")
}

func handleWarn(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	target := opts["user"].UserValue(s)
	reason := opts["reason"].StringValue()

	w := config.Warning{
		Reason:    reason,
		ModID:     i.Member.User.ID,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if storage.DB != nil {
		_ = storage.DB.AddWarning(i.GuildID, target.ID, w)
	}

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	warns := gs.Warnings[target.ID]
	w.ID = len(warns) + 1
	gs.Warnings[target.ID] = append(warns, w)
	gs.Unlock()
	_ = gs.Save()

	respond(s, i, fmt.Sprintf("⚠️ **%s** has been warned (Warning #%d). Reason: %s", target.Username, w.ID, reason), false)
	logModAction(s, i.GuildID, fmt.Sprintf("Warn (#%d)", w.ID), target, i.Member.User, reason, "")
}

func handleWarnings(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	target := opts["user"].UserValue(s)

	var warns []config.Warning
	if storage.DB != nil {
		warns, _ = storage.DB.GetWarnings(i.GuildID, target.ID)
	}
	if len(warns) == 0 {
		gs := storage.GetGuild(i.GuildID)
		warns = gs.Warnings[target.ID]
	}

	if len(warns) == 0 {
		respond(s, i, fmt.Sprintf(" **%s** has no warnings.", target.Username), true)
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 **Warnings for %s** (%d total):\n", target.Username, len(warns)))
	for _, w := range warns {
		ts := w.Timestamp
		if len(ts) >= 10 {
			ts = ts[:10]
		}
		sb.WriteString(fmt.Sprintf("`#%d` — %s (by <@%s> on %s)\n", w.ID, w.Reason, w.ModID, ts))
	}
	respond(s, i, sb.String(), true)
}

func handleClearWarnings(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	target := opts["user"].UserValue(s)

	if storage.DB != nil {
		_ = storage.DB.ClearWarnings(i.GuildID, target.ID)
	}

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	delete(gs.Warnings, target.ID)
	gs.Unlock()
	_ = gs.Save()

	respond(s, i, fmt.Sprintf("🗑️ All warnings cleared for **%s**.", target.Username), false)
}

func handlePurge(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	count := int(opts["count"].IntValue())
	if count < 1 || count > 100 {
		respond(s, i, " Count must be between 1 and 100.", true)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	msgs, err := s.ChannelMessages(i.ChannelID, count, "", "", "")
	if err != nil {
		followup(s, i, fmt.Sprintf(" Failed to fetch messages: %v", err))
		return
	}

	var filterUser *discordgo.User
	if u, ok := opts["user"]; ok {
		filterUser = u.UserValue(s)
	}

	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if filterUser != nil && m.Author.ID != filterUser.ID {
			continue
		}
		ids = append(ids, m.ID)
	}

	if len(ids) == 0 {
		followup(s, i, "No messages found matching criteria.")
		return
	}

	if len(ids) == 1 {
		_ = s.ChannelMessageDelete(i.ChannelID, ids[0])
	} else {
		_ = s.ChannelMessagesBulkDelete(i.ChannelID, ids)
	}

	followup(s, i, fmt.Sprintf("🗑️ Deleted **%d** messages.", len(ids)))
}

func handleSlowmode(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	secs := int(opts["seconds"].IntValue())
	if secs < 0 {
		secs = 0
	}
	if secs > 21600 {
		secs = 21600
	}

	_, err := s.ChannelEdit(i.ChannelID, &discordgo.ChannelEdit{RateLimitPerUser: &secs})
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed: %v", err), true)
		return
	}

	if secs == 0 {
		respond(s, i, "⏱️ Slowmode **disabled**.", false)
	} else {
		respond(s, i, fmt.Sprintf("⏱️ Slowmode set to **%d seconds**.", secs), false)
	}
}

func handleLock(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.ChannelPermissionSet(
		i.ChannelID, i.GuildID,
		discordgo.PermissionOverwriteTypeRole,
		0, discordgo.PermissionSendMessages,
	)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed: %v", err), true)
		return
	}
	respond(s, i, "🔒 Channel **locked**.", false)
}

func handleUnlock(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.ChannelPermissionSet(
		i.ChannelID, i.GuildID,
		discordgo.PermissionOverwriteTypeRole,
		discordgo.PermissionSendMessages, 0,
	)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Failed: %v", err), true)
		return
	}
	respond(s, i, "🔓 Channel **unlocked**.", false)
}

func handleModlog(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	ch := opts["channel"].ChannelValue(s)

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	gs.ModLogChannelOverride = ch.ID
	gs.Unlock()
	_ = gs.Save()

	respond(s, i, fmt.Sprintf("📝 Mod-log channel set to <#%s>.", ch.ID), false)
}

func handleUserinfo(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	var target *discordgo.User
	if u, ok := opts["user"]; ok {
		target = u.UserValue(s)
	} else {
		target = i.Member.User
	}

	member, err := s.GuildMember(i.GuildID, target.ID)
	if err != nil {
		respond(s, i, fmt.Sprintf(" Could not fetch member: %v", err), true)
		return
	}

	joinedAt := "unknown"
	if member.JoinedAt != (time.Time{}) {
		joinedAt = fmt.Sprintf("<t:%d:F>", member.JoinedAt.Unix())
	}
	createdAt := fmt.Sprintf("<t:%d:F>", snowflakeTime(target.ID).Unix())

	roles := "None"
	if len(member.Roles) > 0 {
		r := make([]string, len(member.Roles))
		for idx, rid := range member.Roles {
			r[idx] = fmt.Sprintf("<@&%s>", rid)
		}
		roles = strings.Join(r, ", ")
	}

	warnCount := 0
	if storage.DB != nil {
		if w, err := storage.DB.GetWarnings(i.GuildID, target.ID); err == nil {
			warnCount = len(w)
		}
	}
	if warnCount == 0 {
		gs := storage.GetGuild(i.GuildID)
		warnCount = len(gs.Warnings[target.ID])
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("User Info — %s", target.Username),
		Color: 0x5865F2,
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: target.AvatarURL("256"),
		},
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ID", Value: target.ID, Inline: true},
			{Name: "Account Created", Value: createdAt, Inline: true},
			{Name: "Joined Server", Value: joinedAt, Inline: true},
			{Name: fmt.Sprintf("Roles (%d)", len(member.Roles)), Value: roles},
			{Name: "Warnings", Value: strconv.Itoa(warnCount), Inline: true},
		},
	}

	respondEmbed(s, i, embed, true)
}

func logModAction(s *discordgo.Session, guildID, action string, target, moderator *discordgo.User, reason, duration string) {
	if storage.DB != nil {
		_ = storage.DB.AddModCase(guildID, storage.ModCase{
			GuildID:   guildID,
			UserID:    target.ID,
			ModID:     moderator.ID,
			Action:    action,
			Reason:    reason,
			Duration:  duration,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	gs := storage.GetGuild(guildID)
	logCh := config.EffectiveModLogChannel(storage.Cfg, gs)
	if logCh == "" {
		return
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("Moderation — %s", action),
		Color: 0xED4245,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "User", Value: fmt.Sprintf("%s (`%s`)", target.Username, target.ID), Inline: true},
			{Name: "Moderator", Value: fmt.Sprintf("%s (`%s`)", moderator.Username, moderator.ID), Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}
	if reason != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Reason", Value: reason})
	}
	if duration != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "Duration", Value: duration, Inline: true})
	}

	_, _ = s.ChannelMessageSendEmbed(logCh, embed)
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func snowflakeTime(id string) time.Time {
	n, _ := strconv.ParseInt(id, 10, 64)
	ms := (n >> 22) + 1420070400000
	return time.Unix(ms/1000, (ms%1000)*1e6)
}
