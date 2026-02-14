package music

import (
	"fmt"
	"log"
	"sync"
	"time"

	"discord-bot/config"

	"github.com/bwmarrin/discordgo"
)

type Song struct {
	Title     string
	URL       string
	StreamURL string
	Duration  int
	AddedBy   string
}

type Backend interface {
	Name() string

	ResolveSong(query string) (*Song, error)

	Play(vc *discordgo.VoiceConnection, song *Song, volume int, done chan<- struct{})
	Stop()
	SetVolume(vol int)
	Cleanup()
}

type GuildPlayer struct {
	mu      sync.Mutex
	GuildID string

	Queue      []*Song
	NowPlaying *Song
	Playing    bool
	Paused     bool
	Volume     int

	VoiceConn *discordgo.VoiceConnection
	backend   Backend
	session   *discordgo.Session
	stopCh    chan struct{}
}

type Manager struct {
	mu      sync.RWMutex
	players map[string]*GuildPlayer
	backend Backend
	session *discordgo.Session
	cfg     *config.MusicConfig
}

func NewManager(s *discordgo.Session, cfg *config.MusicConfig) (*Manager, error) {
	var b Backend
	var err error

	switch cfg.Backend {
	case "direct":
		b, err = NewDirectBackend(&cfg.Direct)
		if err != nil {
			return nil, fmt.Errorf("direct backend: %w", err)
		}
		log.Printf("[Music] Using direct backend (yt-dlp: %s, ffmpeg: %s)", cfg.Direct.YTDLPPath, cfg.Direct.FFmpegPath)

	case "lavalink":
		b, err = NewLavalinkBackend(&cfg.Lavalink, s)
		if err != nil {
			return nil, fmt.Errorf("lavalink backend: %w", err)
		}
		log.Printf("[Music] Using Lavalink backend (%s:%d)", cfg.Lavalink.Host, cfg.Lavalink.Port)

	default:
		return nil, fmt.Errorf("unknown music backend: %q (use \"direct\" or \"lavalink\")", cfg.Backend)
	}

	return &Manager{
		players: make(map[string]*GuildPlayer),
		backend: b,
		session: s,
		cfg:     cfg,
	}, nil
}

func (m *Manager) GetPlayer(guildID string) *GuildPlayer {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.players[guildID]
	if !ok {
		p = &GuildPlayer{
			GuildID: guildID,
			Volume:  m.cfg.DefaultVolume,
			backend: m.backend,
			session: m.session,
		}
		m.players[guildID] = p
	}
	return p
}

func (m *Manager) BackendName() string {
	return m.backend.Name()
}

func (m *Manager) ResolveSong(query string) (*Song, error) {
	return m.backend.ResolveSong(query)
}

func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.players {
		p.Stop()
	}
	m.backend.Cleanup()
}

func (p *GuildPlayer) Mu() *sync.Mutex {
	return &p.mu
}

func (p *GuildPlayer) JoinChannel(guildID, channelID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, isLavalink := p.backend.(*LavalinkBackend)

	if p.VoiceConn != nil {

		if p.VoiceConn.ChannelID == channelID {
			return nil
		}
		p.VoiceConn.Disconnect()
	}

	ClearVoiceInfo(guildID)

	vc, err := p.session.ChannelVoiceJoin(guildID, channelID, false, false)
	if err != nil {
		return err
	}
	p.VoiceConn = vc

	if isLavalink {
		time.Sleep(500 * time.Millisecond)
		vc.Close()
		log.Printf("[Music] Closed discordgo voice WS for guild %s (Lavalink will manage voice)", guildID)
	}

	return nil
}

func (p *GuildPlayer) Enqueue(song *Song, maxQueueSize int) (position int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.Queue) >= maxQueueSize {
		return 0, fmt.Errorf("queue is full (%d/%d)", len(p.Queue), maxQueueSize)
	}
	p.Queue = append(p.Queue, song)
	return len(p.Queue), nil
}

func (p *GuildPlayer) PlayNext() {
	p.mu.Lock()

	if p.VoiceConn == nil {
		p.Playing = false
		p.mu.Unlock()
		return
	}

	if len(p.Queue) == 0 {
		p.NowPlaying = nil
		p.Playing = false
		p.mu.Unlock()

		go func() {
			time.Sleep(2 * time.Minute)
			p.mu.Lock()
			if !p.Playing && p.VoiceConn != nil {
				p.VoiceConn.Disconnect()
				p.VoiceConn = nil
			}
			p.mu.Unlock()
		}()
		return
	}

	song := p.Queue[0]
	p.Queue = p.Queue[1:]
	p.NowPlaying = song
	p.Playing = true
	p.Paused = false

	vc := p.VoiceConn
	vol := p.Volume
	p.stopCh = make(chan struct{})
	p.mu.Unlock()

	done := make(chan struct{})
	go p.backend.Play(vc, song, vol, done)

	go func() {
		<-done
		p.mu.Lock()

		if p.NowPlaying == song {
			p.mu.Unlock()
			p.PlayNext()
		} else {
			p.mu.Unlock()
		}
	}()
}

func (p *GuildPlayer) Skip() *Song {
	p.mu.Lock()
	skipped := p.NowPlaying
	p.mu.Unlock()

	p.backend.Stop()

	return skipped
}

func (p *GuildPlayer) Stop() {
	p.mu.Lock()
	p.Queue = nil
	p.NowPlaying = nil
	p.Playing = false
	p.Paused = false
	vc := p.VoiceConn
	p.mu.Unlock()

	p.backend.Stop()

	if vc != nil {
		vc.Disconnect()
		p.mu.Lock()
		p.VoiceConn = nil
		p.mu.Unlock()
		log.Printf("[Music] Disconnected from voice in guild %s", vc.GuildID)
	}
}

func (p *GuildPlayer) SetVolume(vol int) {
	p.mu.Lock()
	p.Volume = vol
	p.mu.Unlock()
	p.backend.SetVolume(vol)
}
func (p *GuildPlayer) Backend() Backend {
	return p.backend
}

func GetVoiceChannelOfUser(s *discordgo.Session, guildID, userID string) string {
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return ""
	}
	for _, vs := range guild.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID
		}
	}
	return ""
}
