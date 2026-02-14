package music

import (
	"bytes"
	"discord-bot/config"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/ogg"
)

type DirectBackend struct {
	ytdlpPath  string
	ffmpegPath string

	mu        sync.Mutex
	ffmpegCmd *exec.Cmd
	stopFlag  bool
	volume    int
}

func NewDirectBackend(cfg *config.DirectMusicConfig) (*DirectBackend, error) {
	if _, err := exec.LookPath(cfg.YTDLPPath); err != nil {
		return nil, fmt.Errorf("yt-dlp not found at %q: %w", cfg.YTDLPPath, err)
	}
	if _, err := exec.LookPath(cfg.FFmpegPath); err != nil {
		return nil, fmt.Errorf("ffmpeg not found at %q: %w", cfg.FFmpegPath, err)
	}

	return &DirectBackend{
		ytdlpPath:  cfg.YTDLPPath,
		ffmpegPath: cfg.FFmpegPath,
		volume:     50,
	}, nil
}

func (d *DirectBackend) Name() string { return "direct" }

func (d *DirectBackend) ResolveSong(query string) (*Song, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}

	if isHTTPURL(q) && looksLikeDirectAudioURL(q) {
		title := deriveTitleFromURL(q)
		return &Song{Title: title, URL: q, StreamURL: q, Duration: 0}, nil
	}

	provider, term := splitProviderPrefix(q)

	if provider == "bc" {
		term = strings.TrimSpace(term)
		if !isHTTPURL(term) {
			return nil, fmt.Errorf("bc: expects a Bandcamp URL (example: bc: https://artist.bandcamp.com/track/...)")
		}
		return d.resolveWithYTDLP(term)
	}

	var candidates []string
	if isHTTPURL(q) {
		candidates = []string{q}
	} else {
		term = strings.TrimSpace(term)
		if term == "" {
			return nil, fmt.Errorf("missing search query")
		}

		switch provider {
		case "sc":
			candidates = []string{"scsearch5:" + term}
		case "yt":
			candidates = []string{"ytsearch1:" + term}
		case "":
			candidates = []string{"scsearch5:" + term}
		default:
			return nil, fmt.Errorf("unknown provider %q (use yt:, sc:, bc:)", provider)
		}
	}

	var lastErr error
	for _, cand := range candidates {
		s, err := d.resolveWithYTDLP(cand)
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (d *DirectBackend) resolveWithYTDLP(query string) (*Song, error) {

	isSC := strings.HasPrefix(query, "scsearch") || strings.Contains(query, "soundcloud.com")

	args := []string{
		"--no-playlist",
		"--dump-json",
		"--no-warnings",
		"--no-check-certificates",
	}

	if isSC {
		args = append(args,
			"-f", "http_mp3_128/http_mp3_64/bestaudio/best",
		)
	} else {
		args = append(args,
			"-f", "bestaudio/best",
			"--format-sort", "proto:https,ext:m4a:mp3:opus:ogg,aext:m4a:mp3:opus:ogg,acodec:opus:aac",
			"--extractor-args", "youtube:player_client=android",
			"--extractor-args", "youtube:player_skip=webpage,configs,js",
		)
	}

	args = append(args, query)

	cmd := exec.Command(d.ytdlpPath, args...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("yt-dlp failed: %s", errMsg)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("yt-dlp returned empty output (stderr: %s)", strings.TrimSpace(stderr.String()))
	}

	var info struct {
		Title        string  `json:"title"`
		URL          string  `json:"url"`
		Duration     float64 `json:"duration"`
		WebPage      string  `json:"webpage_url"`
		Extractor    string  `json:"extractor"`
		ExtractorKey string  `json:"extractor_key"`
	}
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		preview := output
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		return nil, fmt.Errorf("yt-dlp JSON parse error: %w (output preview: %s)", err, preview)
	}

	streamLower := strings.ToLower(info.URL)
	if strings.Contains(streamLower, "cf-preview-media.sndcdn.com") || int(info.Duration) > 0 && int(info.Duration) < 45 {
		return nil, fmt.Errorf("soundcloud returned preview stream (%ds). Try another result / different query.", int(info.Duration))
	}

	return &Song{
		Title:     info.Title,
		URL:       info.WebPage,
		StreamURL: info.URL,
		Duration:  int(info.Duration),
	}, nil
}

func splitProviderPrefix(q string) (provider string, term string) {
	lower := strings.ToLower(strings.TrimSpace(q))
	for _, p := range []string{"yt:", "sc:", "bc:"} {
		if strings.HasPrefix(lower, p) {
			return strings.TrimSuffix(p, ":"), strings.TrimSpace(q[len(p):])
		}
	}
	return "", q
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func looksLikeDirectAudioURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	switch {
	case strings.HasSuffix(p, ".mp3"),
		strings.HasSuffix(p, ".m4a"),
		strings.HasSuffix(p, ".aac"),
		strings.HasSuffix(p, ".ogg"),
		strings.HasSuffix(p, ".opus"),
		strings.HasSuffix(p, ".flac"),
		strings.HasSuffix(p, ".wav"),
		strings.HasSuffix(p, ".webm"),
		strings.HasSuffix(p, ".m3u8"),
		strings.HasSuffix(p, ".pls"),
		strings.HasSuffix(p, ".m3u"),
		strings.HasSuffix(p, ".mpd"):
		return true
	}
	low := strings.ToLower(raw)
	return strings.Contains(low, "icecast") || strings.Contains(low, "stream")
}

func deriveTitleFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "Direct stream"
	}
	base := path.Base(u.Path)
	base = strings.TrimSpace(base)
	if base == "" || base == "/" || base == "." {
		return "Direct stream"
	}
	return base
}

func (d *DirectBackend) Play(vc *discordgo.VoiceConnection, song *Song, volume int, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	d.mu.Lock()
	d.stopFlag = false
	d.volume = volume
	d.mu.Unlock()

	streamURL := song.StreamURL
	if streamURL == "" {
		log.Printf("[Music] No stream URL for %s, re-resolving...", song.Title)
		resolved, err := d.ResolveSong(song.URL)
		if err != nil {
			log.Printf("[Music] Failed to resolve: %v", err)
			return
		}
		streamURL = resolved.StreamURL
	}

	d.mu.Lock()
	vol := d.volume
	d.mu.Unlock()
	volFilter := fmt.Sprintf("volume=%.2f", float64(vol)/100.0)

	ffmpegCmd := exec.Command(d.ffmpegPath,
		"-loglevel", "error",
		"-rw_timeout", "15000000",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_at_eof", "1",
		"-reconnect_delay_max", "5",
		"-i", streamURL,

		"-ar", "48000",
		"-ac", "2",

		"-af", volFilter,
		"-c:a", "libopus",
		"-b:a", "96K",
		"-vbr", "on",
		"-frame_duration", "20",
		"-application", "audio",
		"-vn",
		"-f", "ogg",
		"pipe:1",
	)

	ffmpegOut, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		log.Printf("[Music] ffmpeg stdout pipe error: %v", err)
		return
	}

	ffmpegErr, _ := ffmpegCmd.StderrPipe()

	if err := ffmpegCmd.Start(); err != nil {
		log.Printf("[Music] ffmpeg start error: %v", err)
		return
	}

	go func() {
		if ffmpegErr == nil {
			return
		}
		b, _ := io.ReadAll(ffmpegErr)
		s := strings.TrimSpace(string(b))
		if s != "" {
			log.Printf("[Music] ffmpeg stderr: %s", s)
		}
	}()

	d.mu.Lock()
	d.ffmpegCmd = ffmpegCmd
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.ffmpegCmd = nil
		stopped := d.stopFlag
		d.mu.Unlock()

		if stopped && ffmpegCmd.Process != nil {
			_ = ffmpegCmd.Process.Kill()
		}
	}()

	if err := vc.Speaking(true); err != nil {
		log.Printf("[Music] Speaking error: %v", err)
		return
	}
	defer func() { _ = vc.Speaking(false) }()

	dec := ogg.NewPacketDecoder(ogg.NewDecoder(ffmpegOut))

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		d.mu.Lock()
		stopped := d.stopFlag
		d.mu.Unlock()
		if stopped || !vc.Ready {
			return
		}

		packet, _, err := dec.Decode()
		if err != nil {
			if err == io.EOF {
				log.Printf("[Music] stream ended (ffmpeg output EOF)")
			} else {
				log.Printf("[Music] ogg decode error: %v", err)
			}
			return
		}

		if bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags")) {
			continue
		}
		if len(packet) == 0 {
			continue
		}

		<-ticker.C
		select {
		case vc.OpusSend <- packet:
		default:
		}
	}
}

func (d *DirectBackend) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopFlag = true
	if d.ffmpegCmd != nil && d.ffmpegCmd.Process != nil {
		_ = d.ffmpegCmd.Process.Kill()
	}
}

func (d *DirectBackend) SetVolume(vol int) {
	d.mu.Lock()
	d.volume = vol
	d.mu.Unlock()
}

func (d *DirectBackend) Cleanup() { d.Stop() }
