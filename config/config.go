package config

import (
	"encoding/json"
	"os"
	"sync"
)

type Config struct {
	Discord     DiscordConfig     `json:"discord"`
	YouTube     YouTubeConfig     `json:"youtube"`
	Database    DatabaseConfig    `json:"database"`
	Minecraft   MinecraftConfig   `json:"minecraft"`
	Permissions PermissionsConfig `json:"permissions"`
	Music       MusicConfig       `json:"music"`
	Moderation  ModerationConfig  `json:"moderation"`
	Tickets     TicketsConfig     `json:"tickets"`
}

type DiscordConfig struct {
	Token   string `json:"token"`
	GuildID string `json:"guild_id"`
	Prefix  string `json:"prefix"`
}

type YouTubeConfig struct {
	APIKey string `json:"api_key"`
}

type DatabaseConfig struct {
	Driver  string        `json:"driver"`
	SQLite  SQLiteConfig  `json:"sqlite"`
	MongoDB MongoDBConfig `json:"mongodb"`
}

type SQLiteConfig struct {
	Path string `json:"path"`
}

type MongoDBConfig struct {
	URI      string `json:"uri"`
	Database string `json:"database"`
}

type MinecraftConfig struct {
	Enabled      bool   `json:"enabled"`
	RCONAddress  string `json:"rcon_ip"`
	RCONPort     int    `json:"rcon_port"`
	RCONPassword string `json:"rcon_password"`
}

type PermissionsConfig struct {
	AdminRoles     []string `json:"admin_roles"`
	ModeratorRoles []string `json:"moderator_roles"`
	DJRoles        []string `json:"dj_roles"`
}

type MusicConfig struct {
	Enabled         bool   `json:"enabled"`
	Backend         string `json:"backend"`
	MaxQueueSize    int    `json:"max_queue_size"`
	MaxSongDuration int    `json:"max_song_duration"`
	AllowPlaylists  bool   `json:"allow_playlists"`
	DefaultVolume   int    `json:"default_volume"`

	Direct   DirectMusicConfig   `json:"direct"`
	Lavalink LavalinkMusicConfig `json:"lavalink"`
}

type DirectMusicConfig struct {
	YTDLPPath  string `json:"ytdlp_path"`
	FFmpegPath string `json:"ffmpeg_path"`
}

type LavalinkMusicConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Secure   bool   `json:"secure"`
}

type ModerationConfig struct {
	ModLogChannel string        `json:"mod_log_channel"`
	MuteRole      string        `json:"mute_role"`
	AutoMod       AutoModConfig `json:"auto_mod"`
}

type AutoModConfig struct {
	Enabled         bool `json:"enabled"`
	MaxMentions     int  `json:"max_mentions"`
	MaxLines        int  `json:"max_lines"`
	AntiSpamSeconds int  `json:"anti_spam_seconds"`
	AntiSpamCount   int  `json:"anti_spam_count"`
}

type TicketsConfig struct {
	Enabled         bool             `json:"enabled"`
	PanelChannel    string           `json:"panel_channel"`
	LogChannel      string           `json:"log_channel"`
	StaffRole       string           `json:"staff_role"`
	DiscordCategory string           `json:"discord_category"`
	MaxOpenPerUser  int              `json:"max_open_per_user"`
	Categories      []TicketCategory `json:"categories"`
}

type TicketCategory struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Emoji         string              `json:"emoji"`
	Description   string              `json:"description"`
	Subcategories []TicketSubcategory `json:"subcategories"`
}

type TicketSubcategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Emoji       string `json:"emoji"`
	Description string `json:"description"`
}

type GuildState struct {
	mu       sync.RWMutex
	filePath string

	GuildID string `json:"guild_id"`

	ModLogChannelOverride string `json:"mod_log_channel_override,omitempty"`
	MuteRoleOverride      string `json:"mute_role_override,omitempty"`

	TicketRuntime TicketRuntime `json:"ticket_runtime"`

	Warnings map[string][]Warning `json:"warnings"`
}

type TicketRuntime struct {
	PanelChannelOverride    string            `json:"panel_channel_override,omitempty"`
	LogChannelOverride      string            `json:"log_channel_override,omitempty"`
	StaffRoleOverride       string            `json:"staff_role_override,omitempty"`
	DiscordCategoryOverride string            `json:"discord_category_override,omitempty"`
	PanelMessageID          string            `json:"panel_message_id"`
	TicketCounter           int               `json:"ticket_counter"`
	OpenTickets             map[string]Ticket `json:"open_tickets"`

	ExtraCategories []TicketCategory `json:"extra_categories,omitempty"`
}

type Ticket struct {
	ChannelID   string `json:"channel_id"`
	UserID      string `json:"user_id"`
	CategoryID  string `json:"category_id"`
	SubCategory string `json:"sub_category"`
	Number      int    `json:"number"`
	CreatedAt   string `json:"created_at"`
}

type Warning struct {
	ID        int    `json:"id"`
	Reason    string `json:"reason"`
	ModID     string `json:"mod_id"`
	Timestamp string `json:"timestamp"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Tickets.MaxOpenPerUser <= 0 {
		cfg.Tickets.MaxOpenPerUser = 1
	}
	if cfg.Music.MaxQueueSize <= 0 {
		cfg.Music.MaxQueueSize = 100
	}
	if cfg.Music.DefaultVolume <= 0 {
		cfg.Music.DefaultVolume = 50
	}
	if cfg.Music.Backend == "" {
		cfg.Music.Backend = "direct"
	}
	if cfg.Music.Direct.YTDLPPath == "" {
		cfg.Music.Direct.YTDLPPath = "yt-dlp"
	}
	if cfg.Music.Direct.FFmpegPath == "" {
		cfg.Music.Direct.FFmpegPath = "ffmpeg"
	}
	if cfg.Music.Lavalink.Host == "" {
		cfg.Music.Lavalink.Host = "localhost"
	}
	if cfg.Music.Lavalink.Port == 0 {
		cfg.Music.Lavalink.Port = 2333
	}
	if cfg.Music.Lavalink.Password == "" {
		cfg.Music.Lavalink.Password = "youshallnotpass"
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.SQLite.Path == "" {
		cfg.Database.SQLite.Path = "data/bot.db"
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config, path string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func LoadGuildState(guildID string) *GuildState {
	dir := "data/guilds"
	_ = os.MkdirAll(dir, 0755)
	path := dir + "/" + guildID + ".json"

	gs := &GuildState{
		GuildID:  guildID,
		filePath: path,
		Warnings: make(map[string][]Warning),
		TicketRuntime: TicketRuntime{
			OpenTickets: make(map[string]Ticket),
		},
	}

	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, gs)
	}
	gs.filePath = path
	if gs.Warnings == nil {
		gs.Warnings = make(map[string][]Warning)
	}
	if gs.TicketRuntime.OpenTickets == nil {
		gs.TicketRuntime.OpenTickets = make(map[string]Ticket)
	}
	return gs
}

func (gs *GuildState) Save() error {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	data, err := json.MarshalIndent(gs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(gs.filePath, data, 0644)
}

func (gs *GuildState) Lock()   { gs.mu.Lock() }
func (gs *GuildState) Unlock() { gs.mu.Unlock() }

func MergedTicketCategories(cfg *Config, gs *GuildState) []TicketCategory {
	all := make([]TicketCategory, 0, len(cfg.Tickets.Categories)+len(gs.TicketRuntime.ExtraCategories))
	all = append(all, cfg.Tickets.Categories...)
	all = append(all, gs.TicketRuntime.ExtraCategories...)
	return all
}

func EffectiveTicketPanelChannel(cfg *Config, gs *GuildState) string {
	if gs.TicketRuntime.PanelChannelOverride != "" {
		return gs.TicketRuntime.PanelChannelOverride
	}
	return cfg.Tickets.PanelChannel
}

func EffectiveTicketLogChannel(cfg *Config, gs *GuildState) string {
	if gs.TicketRuntime.LogChannelOverride != "" {
		return gs.TicketRuntime.LogChannelOverride
	}
	return cfg.Tickets.LogChannel
}

func EffectiveTicketStaffRole(cfg *Config, gs *GuildState) string {
	if gs.TicketRuntime.StaffRoleOverride != "" {
		return gs.TicketRuntime.StaffRoleOverride
	}
	return cfg.Tickets.StaffRole
}

func EffectiveTicketCategory(cfg *Config, gs *GuildState) string {
	if gs.TicketRuntime.DiscordCategoryOverride != "" {
		return gs.TicketRuntime.DiscordCategoryOverride
	}
	return cfg.Tickets.DiscordCategory
}

func EffectiveModLogChannel(cfg *Config, gs *GuildState) string {
	if gs.ModLogChannelOverride != "" {
		return gs.ModLogChannelOverride
	}
	return cfg.Moderation.ModLogChannel
}
