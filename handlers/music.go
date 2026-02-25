package handlers

import (
	"fmt"
	"strings"

	"discord-bot/lang"
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
		respond(s, i, lang.T("music_disabled"), true)
		return
	}
	if MusicMgr == nil {
		respond(s, i, lang.T("music_init_failed"), true)
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
		respond(s, i, lang.T("music_not_in_vc"), true)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	player := MusicMgr.GetPlayer(i.GuildID)

	song, err := MusicMgr.ResolveSong(query)
	if err != nil {
		followup(s, i, lang.T("music_song_not_found", "error", err.Error()))
		return
	}

	if cfg.Music.MaxSongDuration > 0 && song.Duration > cfg.Music.MaxSongDuration {
		dur := fmt.Sprintf("%d:%02d", song.Duration/60, song.Duration%60)
		maxDur := fmt.Sprintf("%d:%02d", cfg.Music.MaxSongDuration/60, cfg.Music.MaxSongDuration%60)
		followup(s, i, lang.T("music_song_too_long", "duration", dur, "max_duration", maxDur))
		return
	}

	song.AddedBy = i.Member.User.Username

	if err := player.JoinChannel(i.GuildID, voiceChID); err != nil {
		followup(s, i, lang.T("music_vc_join_failed", "error", err.Error()))
		return
	}

	pos, err := player.Enqueue(song, cfg.Music.MaxQueueSize)
	if err != nil {
		followup(s, i, lang.T("music_queue_full", "error", err.Error()))
		return
	}

	durationStr := fmt.Sprintf("%d:%02d", song.Duration/60, song.Duration%60)

	if !player.Playing {
		player.PlayNext()
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{{
				Title: lang.T("music_now_playing_title"),
				Description: lang.T("music_now_playing_desc",
					"title", song.Title,
					"url", song.URL,
					"duration", durationStr,
					"added_by", song.AddedBy,
					"backend", MusicMgr.BackendName(),
				),
				Color: 0x1DB954,
			}},
		})
	} else {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{{
				Title: lang.T("music_added_to_queue_title"),
				Description: lang.T("music_added_to_queue_desc",
					"title", song.Title,
					"url", song.URL,
					"duration", durationStr,
					"position", fmt.Sprintf("%d", pos),
					"added_by", song.AddedBy,
				),
				Color: 0x5865F2,
			}},
		})
	}
}

func handleSkip(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isDJ(s, i) {
		respond(s, i, lang.T("music_dj_required_skip"), true)
		return
	}

	player := MusicMgr.GetPlayer(i.GuildID)
	if !player.Playing {
		respond(s, i, lang.T("music_nothing_playing"), true)
		return
	}

	skipped := player.Skip()
	if skipped != nil {
		respond(s, i, lang.T("music_skipped_title", "title", skipped.Title), false)
	} else {
		respond(s, i, lang.T("music_skipped"), false)
	}
}

func handleStop(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isDJ(s, i) {
		respond(s, i, lang.T("music_dj_required_stop"), true)
		return
	}

	player := MusicMgr.GetPlayer(i.GuildID)
	player.Stop()
	respond(s, i, lang.T("music_stopped"), false)
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
		sb.WriteString(lang.T("music_queue_now_playing",
			"title", np.Title,
			"url", np.URL,
			"duration", dur,
			"added_by", np.AddedBy,
		))
	} else {
		sb.WriteString(lang.T("music_queue_nothing"))
	}

	if len(queue) == 0 {
		sb.WriteString(lang.T("music_queue_empty"))
	} else {
		sb.WriteString(lang.T("music_queue_header", "count", fmt.Sprintf("%d", len(queue))))
		max := 15
		if len(queue) < max {
			max = len(queue)
		}
		for idx := 0; idx < max; idx++ {
			dur := fmt.Sprintf("%d:%02d", queue[idx].Duration/60, queue[idx].Duration%60)
			sb.WriteString(lang.T("music_queue_entry",
				"pos", fmt.Sprintf("%d", idx+1),
				"title", queue[idx].Title,
				"url", queue[idx].URL,
				"duration", dur,
				"added_by", queue[idx].AddedBy,
			))
		}
		if len(queue) > 15 {
			sb.WriteString(lang.T("music_queue_more", "count", fmt.Sprintf("%d", len(queue)-15)))
		}
	}

	sb.WriteString(lang.T("music_queue_footer",
		"volume", fmt.Sprintf("%d", vol),
		"backend", MusicMgr.BackendName(),
	))

	respondEmbed(s, i, &discordgo.MessageEmbed{
		Title:       lang.T("music_queue_embed_title"),
		Description: sb.String(),
		Color:       0x5865F2,
	}, true)
}

func handleVolume(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isDJ(s, i) {
		respond(s, i, lang.T("music_dj_required_vol"), true)
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
	respond(s, i, lang.T("music_volume_set", "level", fmt.Sprintf("%d", level)), false)
}

func handleNowPlaying(s *discordgo.Session, i *discordgo.InteractionCreate) {
	player := MusicMgr.GetPlayer(i.GuildID)

	player.Mu().Lock()
	np := player.NowPlaying
	vol := player.Volume
	player.Mu().Unlock()

	if np == nil {
		respond(s, i, lang.T("music_nothing_playing"), true)
		return
	}

	dur := fmt.Sprintf("%d:%02d", np.Duration/60, np.Duration%60)

	respondEmbed(s, i, &discordgo.MessageEmbed{
		Title: lang.T("music_nowplaying_embed_title"),
		Description: lang.T("music_nowplaying_embed_desc",
			"title", np.Title,
			"url", np.URL,
			"duration", dur,
			"volume", fmt.Sprintf("%d", vol),
			"added_by", np.AddedBy,
			"backend", MusicMgr.BackendName(),
		),
		Color: 0x1DB954,
	}, false)
}

func handlePause(s *discordgo.Session, i *discordgo.InteractionCreate) {
	player := MusicMgr.GetPlayer(i.GuildID)

	player.Mu().Lock()
	if !player.Playing || player.Paused {
		player.Mu().Unlock()
		respond(s, i, lang.T("music_nothing_to_pause"), true)
		return
	}
	player.Paused = true
	backend := player.Backend()
	player.Mu().Unlock()

	if b, ok := backend.(interface{ SetPaused(bool) }); ok {
		b.SetPaused(true)
	}

	respond(s, i, lang.T("music_paused"), false)
}

func handleResume(s *discordgo.Session, i *discordgo.InteractionCreate) {
	player := MusicMgr.GetPlayer(i.GuildID)

	player.Mu().Lock()
	if !player.Paused {
		player.Mu().Unlock()
		respond(s, i, lang.T("music_not_paused"), true)
		return
	}
	player.Paused = false
	backend := player.Backend()
	player.Mu().Unlock()

	if b, ok := backend.(interface{ SetPaused(bool) }); ok {
		b.SetPaused(false)
	}

	respond(s, i, lang.T("music_resumed"), false)
}
