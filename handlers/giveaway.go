package handlers

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"discord-bot/config"
	"discord-bot/lang"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

var (
	giveawayTimers   = make(map[string]*time.Timer)
	giveawayTimersMu sync.Mutex
)

func giveawayCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "giveaway",
			Description:              "Giveaway management",
			DefaultMemberPermissions: &adminPerm,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "create",
					Description: "Create a new giveaway",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to post the giveaway in", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "prize", Description: "What are you giving away?", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "duration", Description: "Duration (e.g. 10m, 2h, 1d)", Required: true},
						{Type: discordgo.ApplicationCommandOptionInteger, Name: "winners", Description: "Number of winners (default: 1)"},
					},
				},
				{
					Name:        "end",
					Description: "End a giveaway early and pick winners now",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "giveaway_id", Description: "Giveaway ID (from /giveaway list)", Required: true},
					},
				},
				{
					Name:        "reroll",
					Description: "Reroll the winners of an ended giveaway",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "giveaway_id", Description: "Giveaway ID", Required: true},
					},
				},
				{
					Name:        "list",
					Description: "List all active giveaways",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
			},
		},
	}
}

func handleGiveawayCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(s, i) {
		respond(s, i, lang.T("no_permission"), true)
		return
	}
	sub := i.ApplicationCommandData().Options[0]

	switch sub.Name {
	case "create":
		handleGiveawayCreate(s, i, sub.Options)
	case "end":
		handleGiveawayEnd(s, i, sub.Options)
	case "reroll":
		handleGiveawayReroll(s, i, sub.Options)
	case "list":
		handleGiveawayList(s, i)
	}
}

func handleGiveawayCreate(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	ch := om["channel"].ChannelValue(s)
	prize := om["prize"].StringValue()
	durStr := om["duration"].StringValue()

	winners := int64(1)
	if w, ok := om["winners"]; ok {
		winners = w.IntValue()
		if winners < 1 {
			winners = 1
		}
		if winners > 20 {
			winners = 20
		}
	}

	dur, err := parseDuration(durStr)
	if err != nil || dur <= 0 {
		respond(s, i, lang.T("giveaway_invalid_duration"), true)
		return
	}

	endsAt := time.Now().Add(dur)
	hostID := i.Member.User.ID

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	giveawayID := fmt.Sprintf("gw%d", len(gs.Giveaways)+1)
	gs.Unlock()

	embed := buildGiveawayEmbed(prize, hostID, int(winners), endsAt, 0)
	msg, err := s.ChannelMessageSendComplex(ch.ID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    lang.T("giveaway_embed_enter_btn"),
						Style:    discordgo.PrimaryButton,
						CustomID: "giveaway_enter:" + giveawayID,
					},
				},
			},
		},
	})
	if err != nil {
		respond(s, i, lang.T("giveaway_post_failed", "error", err.Error()), true)
		return
	}

	gw := config.Giveaway{
		ID:         giveawayID,
		GuildID:    i.GuildID,
		ChannelID:  ch.ID,
		MessageID:  msg.ID,
		Prize:      prize,
		Winners:    int(winners),
		EndsAt:     endsAt.Format(time.RFC3339),
		HostID:     hostID,
		Ended:      false,
		EntrantIDs: map[string]bool{},
	}

	gs.Lock()
	gs.Giveaways = append(gs.Giveaways, gw)
	gs.Unlock()
	_ = gs.Save()

	scheduleGiveaway(s, i.GuildID, giveawayID, dur)

	respond(s, i, lang.T("giveaway_started",
		"prize", prize,
		"id", giveawayID,
		"channel_id", ch.ID,
		"timestamp", fmt.Sprintf("%d", endsAt.Unix()),
		"winners", fmt.Sprintf("%d", winners),
	), true)
}

func handleGiveawayEnd(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	giveawayID := om["giveaway_id"].StringValue()

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	var found *config.Giveaway
	for idx := range gs.Giveaways {
		if gs.Giveaways[idx].ID == giveawayID {
			found = &gs.Giveaways[idx]
			break
		}
	}
	if found == nil {
		gs.Unlock()
		respond(s, i, lang.T("giveaway_not_found", "id", giveawayID), true)
		return
	}
	if found.Ended {
		gs.Unlock()
		respond(s, i, lang.T("giveaway_already_ended"), true)
		return
	}
	gs.Unlock()

	cancelGiveawayTimer(i.GuildID, giveawayID)
	endGiveaway(s, i.GuildID, giveawayID)
	respond(s, i, lang.T("giveaway_ended_early", "id", giveawayID), true)
}

func handleGiveawayReroll(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	om := subOptMap(opts)
	giveawayID := om["giveaway_id"].StringValue()

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	var found *config.Giveaway
	for idx := range gs.Giveaways {
		if gs.Giveaways[idx].ID == giveawayID {
			found = &gs.Giveaways[idx]
			break
		}
	}
	if found == nil {
		gs.Unlock()
		respond(s, i, lang.T("giveaway_not_found_short", "id", giveawayID), true)
		return
	}
	if !found.Ended {
		gs.Unlock()
		respond(s, i, lang.T("giveaway_still_active"), true)
		return
	}
	entrants := make([]string, 0, len(found.EntrantIDs))
	for uid := range found.EntrantIDs {
		entrants = append(entrants, uid)
	}
	numWinners := found.Winners
	channelID := found.ChannelID
	prize := found.Prize
	gs.Unlock()

	if len(entrants) == 0 {
		respond(s, i, lang.T("giveaway_no_entrants"), true)
		return
	}

	winners := pickWinners(entrants, numWinners)
	mentions := make([]string, len(winners))
	for idx, w := range winners {
		mentions[idx] = fmt.Sprintf("<@%s>", w)
	}
	mentionsStr := strings.Join(mentions, ", ")

	_, _ = s.ChannelMessageSend(channelID, lang.T("giveaway_reroll_announce", "prize", prize, "mentions", mentionsStr))
	respond(s, i, lang.T("giveaway_rerolled", "mentions", mentionsStr), true)
}

func handleGiveawayList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	gs := storage.GetGuild(i.GuildID)
	gs.Lock()
	giveaways := make([]config.Giveaway, len(gs.Giveaways))
	copy(giveaways, gs.Giveaways)
	gs.Unlock()

	var active []config.Giveaway
	for _, gw := range giveaways {
		if !gw.Ended {
			active = append(active, gw)
		}
	}

	if len(active) == 0 {
		respond(s, i, lang.T("giveaway_none_active"), true)
		return
	}

	var sb strings.Builder
	sb.WriteString(lang.T("giveaway_list_header"))
	for _, gw := range active {
		endsAt, _ := time.Parse(time.RFC3339, gw.EndsAt)
		sb.WriteString(lang.T("giveaway_list_entry",
			"id", gw.ID,
			"prize", gw.Prize,
			"winners", fmt.Sprintf("%d", gw.Winners),
			"entries", fmt.Sprintf("%d", len(gw.EntrantIDs)),
			"timestamp", fmt.Sprintf("%d", endsAt.Unix()),
			"channel_id", gw.ChannelID,
		))
	}
	respond(s, i, sb.String(), true)
}

func HandleGiveawayEnter(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.SplitN(i.MessageComponentData().CustomID, ":", 2)
	if len(parts) != 2 {
		return
	}
	giveawayID := parts[1]
	userID := i.Member.User.ID

	gs := storage.GetGuild(i.GuildID)
	gs.Lock()

	var gw *config.Giveaway
	for idx := range gs.Giveaways {
		if gs.Giveaways[idx].ID == giveawayID {
			gw = &gs.Giveaways[idx]
			break
		}
	}
	if gw == nil || gw.Ended {
		gs.Unlock()
		respond(s, i, lang.T("giveaway_already_ended_btn"), true)
		return
	}
	if gw.EntrantIDs == nil {
		gw.EntrantIDs = make(map[string]bool)
	}

	if gw.EntrantIDs[userID] {
		delete(gw.EntrantIDs, userID)
		gs.Unlock()
		_ = gs.Save()
		go updateGiveawayMessage(s, i.GuildID, giveawayID)
		respond(s, i, lang.T("giveaway_left"), true)
	} else {
		gw.EntrantIDs[userID] = true
		gs.Unlock()
		_ = gs.Save()
		go updateGiveawayMessage(s, i.GuildID, giveawayID)
		respond(s, i, lang.T("giveaway_entered"), true)
	}
}

func updateGiveawayMessage(s *discordgo.Session, guildID, giveawayID string) {
	gs := storage.GetGuild(guildID)
	gs.Lock()
	var gw *config.Giveaway
	for idx := range gs.Giveaways {
		if gs.Giveaways[idx].ID == giveawayID {
			gw = &gs.Giveaways[idx]
			break
		}
	}
	if gw == nil {
		gs.Unlock()
		return
	}
	prize := gw.Prize
	hostID := gw.HostID
	winners := gw.Winners
	endsAt, _ := time.Parse(time.RFC3339, gw.EndsAt)
	entrantCount := len(gw.EntrantIDs)
	channelID := gw.ChannelID
	messageID := gw.MessageID
	gs.Unlock()

	embed := buildGiveawayEmbed(prize, hostID, winners, endsAt, entrantCount)
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: channelID,
		ID:      messageID,
		Embeds:  &[]*discordgo.MessageEmbed{embed},
	})
}

func buildGiveawayEmbed(prize, hostID string, winners int, endsAt time.Time, entrants int) *discordgo.MessageEmbed {
	winStr := lang.T("giveaway_embed_winner_singular")
	if winners > 1 {
		winStr = lang.T("giveaway_embed_winner_plural")
	}
	return &discordgo.MessageEmbed{
		Title: lang.T("giveaway_embed_title"),
		Description: lang.T("giveaway_embed_description",
			"prize", prize,
			"winners", fmt.Sprintf("%d", winners),
			"winner_word", winStr,
			"host_id", hostID,
			"entries", fmt.Sprintf("%d", entrants),
			"timestamp", fmt.Sprintf("%d", endsAt.Unix()),
		),
		Color: 0xFF73FA,
		Footer: &discordgo.MessageEmbedFooter{
			Text: lang.T("giveaway_embed_footer", "time", endsAt.UTC().Format("Jan 02, 2006 15:04")),
		},
		Timestamp: endsAt.Format(time.RFC3339),
	}
}

func scheduleGiveaway(s *discordgo.Session, guildID, giveawayID string, dur time.Duration) {
	key := guildID + ":" + giveawayID
	t := time.AfterFunc(dur, func() {
		endGiveaway(s, guildID, giveawayID)
	})
	giveawayTimersMu.Lock()
	giveawayTimers[key] = t
	giveawayTimersMu.Unlock()
}

func cancelGiveawayTimer(guildID, giveawayID string) {
	key := guildID + ":" + giveawayID
	giveawayTimersMu.Lock()
	if t, ok := giveawayTimers[key]; ok {
		t.Stop()
		delete(giveawayTimers, key)
	}
	giveawayTimersMu.Unlock()
}

func endGiveaway(s *discordgo.Session, guildID, giveawayID string) {
	gs := storage.GetGuild(guildID)
	gs.Lock()
	var gw *config.Giveaway
	for idx := range gs.Giveaways {
		if gs.Giveaways[idx].ID == giveawayID {
			gw = &gs.Giveaways[idx]
			break
		}
	}
	if gw == nil || gw.Ended {
		gs.Unlock()
		return
	}
	gw.Ended = true

	entrants := make([]string, 0, len(gw.EntrantIDs))
	for uid := range gw.EntrantIDs {
		entrants = append(entrants, uid)
	}
	numWinners := gw.Winners
	channelID := gw.ChannelID
	messageID := gw.MessageID
	prize := gw.Prize
	gs.Unlock()
	_ = gs.Save()

	endedEmbed := &discordgo.MessageEmbed{
		Title: lang.T("giveaway_ended_embed_title"),
		Description: lang.T("giveaway_ended_embed_description",
			"prize", prize,
			"entries", fmt.Sprintf("%d", len(entrants)),
		),
		Color: 0x808080,
	}
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: channelID,
		ID:      messageID,
		Embeds:  &[]*discordgo.MessageEmbed{endedEmbed},
		Components: &[]discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    lang.T("giveaway_ended_btn_label"),
						Style:    discordgo.SecondaryButton,
						CustomID: "giveaway_ended_" + giveawayID,
						Disabled: true,
					},
				},
			},
		},
	})

	if len(entrants) == 0 {
		_, _ = s.ChannelMessageSend(channelID, lang.T("giveaway_no_entrants_end", "prize", prize))
		return
	}

	winners := pickWinners(entrants, numWinners)
	mentions := make([]string, len(winners))
	for idx, w := range winners {
		mentions[idx] = fmt.Sprintf("<@%s>", w)
	}
	mentionsStr := strings.Join(mentions, " ")

	msg := lang.T("giveaway_winners_announce", "prize", prize, "mentions", mentionsStr) +
		"\n\n" + lang.T("giveaway_reroll_hint", "id", giveawayID)
	_, _ = s.ChannelMessageSend(channelID, msg)

	gs.Lock()
	for idx := range gs.Giveaways {
		if gs.Giveaways[idx].ID == giveawayID {
			gs.Giveaways[idx].WinnerIDs = winners
			break
		}
	}
	gs.Unlock()
	_ = gs.Save()
}

func pickWinners(entrants []string, count int) []string {
	if count >= len(entrants) {
		return entrants
	}
	shuffled := make([]string, len(entrants))
	copy(shuffled, entrants)
	rand.Shuffle(len(shuffled), func(a, b int) {
		shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
	})
	return shuffled[:count]
}

func RestoreGiveawayTimers(s *discordgo.Session, gs *config.GuildState) {
	gs.Lock()
	guildID := gs.GuildID
	giveaways := make([]config.Giveaway, len(gs.Giveaways))
	copy(giveaways, gs.Giveaways)
	gs.Unlock()

	for _, gw := range giveaways {
		if gw.Ended {
			continue
		}
		endsAt, err := time.Parse(time.RFC3339, gw.EndsAt)
		if err != nil {
			continue
		}
		remaining := time.Until(endsAt)
		if remaining <= 0 {
			go endGiveaway(s, guildID, gw.ID)
		} else {
			scheduleGiveaway(s, guildID, gw.ID, remaining)
		}
	}
}
