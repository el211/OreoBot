package handlers

import (
	"fmt"
	"strings"

	"discord-bot/music"
	"discord-bot/storage"

	"github.com/bwmarrin/discordgo"
)

var MusicMgr *music.Manager

func musicCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name: "play", Description: "Play a song or add it to the queue",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "query", Description: "Song name or YouTube URL", Required: true},
			},
		},
		{Name: "skip", Description: "Skip the current song"},
		{Name: "stop", Description: "Stop playback and clear the queue"},
		{Name: "queue", Description: "Show the current song queue"},
		{
			Name: "volume", Description: "Set the playback volume",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "level", Description: "Volume 0-100", Required: true},
			},
		},
		{Name: "nowplaying", Description: "Show the currently playing song"},
		{Name: "pause", Description: "Pause playback"},
		{Name: "resume", Description: "Resume playback"},
	}
}

func handleMusicCommand(s *discordgo.Session, i *discordgo.InteractionCreate, name string) {
	cfg := storage.Cfg
	if !cfg.Music.Enabled {
		respond(s, i, " Music is disabled in config.json.", true)
		return
	}
	if MusicMgr == nil {
		respond(s, i, " Music system failed to initialise. Check the server logs.", true)
		return
	}

	switch name {
	case "play":
		handlePlay(s, i)
	case "skip":
		handleSkip(s, i)
	case "stop":
		handleStop(s, i)
	case "queue":
		handleQueue(s, i)
	case "volume":
		handleVolume(s, i)
	case "nowplaying":
		handleNowPlaying(s, i)
	case "pause":
		handlePause(s, i)
	case "resume":
		handleResume(s, i)
	}
}

func handlePlay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := optionMap(i)
	query := opts["query"].StringValue()
	cfg := storage.Cfg

	voiceChID := music.GetVoiceChannelOfUser(s, i.GuildID, i.Member.User.ID)
	if voiceChID == "" {
		respond(s, i, " You need to be in a voice channel first!", true)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	player := MusicMgr.GetPlayer(i.GuildID)

	song, err := MusicMgr.ResolveSong(query)
	if err != nil {
		followup(s, i, fmt.Sprintf(" Could not find song: %v", err))
		return
	}

	if cfg.Music.MaxSongDuration > 0 && song.Duration > cfg.Music.MaxSongDuration {
		followup(s, i, fmt.Sprintf(" Song is too long (%d:%02d). Max duration is %d:%02d.",
			song.Duration/60, song.Duration%60,
			cfg.Music.MaxSongDuration/60, cfg.Music.MaxSongDuration%60))
		return
	}

	song.AddedBy = i.Member.User.Username

	if err := player.JoinChannel(i.GuildID, voiceChID); err != nil {
		followup(s, i, fmt.Sprintf(" Could not join voice channel: %v", err))
		return
	}

	pos, err := player.Enqueue(song, cfg.Music.MaxQueueSize)
	if err != nil {
		followup(s, i, fmt.Sprintf(" %v", err))
		return
	}

	durationStr := fmt.Sprintf("%d:%02d", song.Duration/60, song.Duration%60)

	if !player.Playing {
		player.PlayNext()
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{{
				Title:       "🎶 Now Playing",
				Description: fmt.Sprintf("**[%s](%s)**\nDuration: `%s` | Requested by: %s\nBackend: `%s`", song.Title, song.URL, durationStr, song.AddedBy, MusicMgr.BackendName()),
				Color:       0x1DB954,
			}},
		})
	} else {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{{
				Title:       " Added to Queue",
				Description: fmt.Sprintf("**[%s](%s)**\nDuration: `%s` | Position: #%d | Requested by: %s", song.Title, song.URL, durationStr, pos, song.AddedBy),
				Color:       0x5865F2,
			}},
		})
	}
}

func handleSkip(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isDJ(s, i) {
		respond(s, i, " You need a DJ role to skip.", true)
		return
	}

	player := MusicMgr.GetPlayer(i.GuildID)
	if !player.Playing {
		respond(s, i, " Nothing is playing.", true)
		return
	}

	skipped := player.Skip()
	if skipped != nil {
		respond(s, i, fmt.Sprintf("⏭️ Skipped **%s**.", skipped.Title), false)
	} else {
		respond(s, i, "⏭️ Skipped.", false)
	}
}

func handleStop(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isDJ(s, i) {
		respond(s, i, " You need a DJ role to stop.", true)
		return
	}

	player := MusicMgr.GetPlayer(i.GuildID)
	player.Stop()
	respond(s, i, "⏹️ Stopped playback and cleared the queue.", false)
}

func handleQueue(s *discordgo.Session, i *discordgo.InteractionCreate) {
	player := MusicMgr.GetPlayer(i.GuildID)

	var sb strings.Builder

	player.Mu().Lock()
	np := player.NowPlaying
	queue := make([]*music.Song, len(player.Queue))
	copy(queue, player.Queue)
	vol := player.Volume
	player.Mu().Unlock()

	if np != nil {
		dur := fmt.Sprintf("%d:%02d", np.Duration/60, np.Duration%60)
		sb.WriteString(fmt.Sprintf("🎵 **Now Playing:** [%s](%s) [`%s`] (by %s)\n\n", np.Title, np.URL, dur, np.AddedBy))
	} else {
		sb.WriteString("Nothing is playing.\n\n")
	}

	if len(queue) == 0 {
		sb.WriteString("Queue is empty.")
	} else {
		sb.WriteString(fmt.Sprintf("**Queue** (%d songs):\n", len(queue)))
		max := 15
		if len(queue) < max {
			max = len(queue)
		}
		for idx := 0; idx < max; idx++ {
			dur := fmt.Sprintf("%d:%02d", queue[idx].Duration/60, queue[idx].Duration%60)
			sb.WriteString(fmt.Sprintf("`%d.` [%s](%s) [`%s`] (by %s)\n", idx+1, queue[idx].Title, queue[idx].URL, dur, queue[idx].AddedBy))
		}
		if len(queue) > 15 {
			sb.WriteString(fmt.Sprintf("...and %d more\n", len(queue)-15))
		}
	}

	sb.WriteString(fmt.Sprintf("\n🔊 Volume: **%d%%** | Backend: `%s`", vol, MusicMgr.BackendName()))

	respondEmbed(s, i, &discordgo.MessageEmbed{
		Title:       "📋 Music Queue",
		Description: sb.String(),
		Color:       0x5865F2,
	}, true)
}

func handleVolume(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isDJ(s, i) {
		respond(s, i, " You need a DJ role to change volume.", true)
		return
	}

	opts := optionMap(i)
	level := int(opts["level"].IntValue())
	if level < 0 {
		level = 0
	}
	if level > 100 {
		level = 100
	}

	player := MusicMgr.GetPlayer(i.GuildID)
	player.SetVolume(level)
	respond(s, i, fmt.Sprintf("🔊 Volume set to **%d%%**.", level), false)
}

func handleNowPlaying(s *discordgo.Session, i *discordgo.InteractionCreate) {
	player := MusicMgr.GetPlayer(i.GuildID)

	player.Mu().Lock()
	np := player.NowPlaying
	vol := player.Volume
	player.Mu().Unlock()

	if np == nil {
		respond(s, i, "Nothing is playing.", true)
		return
	}

	dur := fmt.Sprintf("%d:%02d", np.Duration/60, np.Duration%60)

	respondEmbed(s, i, &discordgo.MessageEmbed{
		Title:       "🎵 Now Playing",
		Description: fmt.Sprintf("**[%s](%s)**\nDuration: `%s` | Volume: %d%%\nRequested by: %s | Backend: `%s`", np.Title, np.URL, dur, vol, np.AddedBy, MusicMgr.BackendName()),
		Color:       0x1DB954,
	}, false)
}

func handlePause(s *discordgo.Session, i *discordgo.InteractionCreate) {
	player := MusicMgr.GetPlayer(i.GuildID)

	player.Mu().Lock()
	if !player.Playing || player.Paused {
		player.Mu().Unlock()
		respond(s, i, " Nothing to pause.", true)
		return
	}
	player.Paused = true
	backend := player.Backend()
	player.Mu().Unlock()

	if b, ok := backend.(interface{ SetPaused(bool) }); ok {
		b.SetPaused(true)
	}

	respond(s, i, "⏸️ Paused.", false)
}

func handleResume(s *discordgo.Session, i *discordgo.InteractionCreate) {
	player := MusicMgr.GetPlayer(i.GuildID)

	player.Mu().Lock()
	if !player.Paused {
		player.Mu().Unlock()
		respond(s, i, " Not paused.", true)
		return
	}
	player.Paused = false
	backend := player.Backend()
	player.Mu().Unlock()

	if b, ok := backend.(interface{ SetPaused(bool) }); ok {
		b.SetPaused(false)
	}

	respond(s, i, "▶️ Resumed.", false)
}
