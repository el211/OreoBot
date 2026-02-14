package music

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"discord-bot/config"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
)

type LavalinkBackend struct {
	host     string
	port     int
	password string
	secure   bool
	session  *discordgo.Session

	mu             sync.Mutex
	stopFlag       bool
	volume         int
	currentGuildID string

	wsMu      sync.RWMutex
	ws        *websocket.Conn
	llSession string
}

func NewLavalinkBackend(cfg *config.LavalinkMusicConfig, s *discordgo.Session) (*LavalinkBackend, error) {
	lb := &LavalinkBackend{
		host:     cfg.Host,
		port:     cfg.Port,
		password: cfg.Password,
		secure:   cfg.Secure,
		session:  s,
		volume:   50,
	}

	var pingErr error
	for attempt := 1; attempt <= 15; attempt++ {
		pingErr = lb.ping()
		if pingErr == nil {
			break
		}
		log.Printf("[Lavalink] waiting for server at %s:%d (attempt %d/15): %v", cfg.Host, cfg.Port, attempt, pingErr)
		time.Sleep(2 * time.Second)
	}
	if pingErr != nil {
		return nil, fmt.Errorf("cannot reach Lavalink at %s:%d after 30s: %w", cfg.Host, cfg.Port, pingErr)
	}

	if err := lb.connectLLWS(); err != nil {
		return nil, fmt.Errorf("lavalink websocket connect failed: %w", err)
	}

	return lb, nil
}

func (l *LavalinkBackend) Name() string { return "lavalink" }

func (l *LavalinkBackend) baseURL() string {
	scheme := "http"
	if l.secure {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, l.host, l.port)
}

func (l *LavalinkBackend) llWSURL() string {
	scheme := "ws"
	if l.secure {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s:%d/v4/websocket", scheme, l.host, l.port)
}

func (l *LavalinkBackend) ping() error {
	req, _ := http.NewRequest("GET", l.baseURL()+"/version", nil)
	req.Header.Set("Authorization", l.password)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Lavalink returned status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

type llReady struct {
	Op        string `json:"op"`
	SessionID string `json:"sessionId"`
}

func (l *LavalinkBackend) connectLLWS() error {
	u := l.llWSURL()

	headers := http.Header{}
	headers.Set("Authorization", l.password)

	if l.session == nil || l.session.State == nil || l.session.State.User == nil {
		return fmt.Errorf("discord session not ready (State.User is nil)")
	}
	headers.Set("User-Id", l.session.State.User.ID)
	headers.Set("Client-Name", "OreoBot2-Go")

	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := d.Dial(u, headers)
	if err != nil {
		return fmt.Errorf("ws dial %s: %w", u, err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var ready llReady
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("ws read: %w", err)
		}
		if err := json.Unmarshal(msg, &ready); err == nil && strings.EqualFold(ready.Op, "ready") && ready.SessionID != "" {
			break
		}
	}
	_ = conn.SetReadDeadline(time.Time{})

	l.wsMu.Lock()
	oldConn := l.ws
	l.ws = conn
	l.llSession = ready.SessionID
	l.wsMu.Unlock()

	if oldConn != nil {
		_ = oldConn.Close()
	}

	log.Printf("[Lavalink] WS connected. sessionId=%s", ready.SessionID)

	go l.llWSReadLoop()
	return nil
}

func (l *LavalinkBackend) llWSReadLoop() {
	l.wsMu.RLock()
	myConn := l.ws
	l.wsMu.RUnlock()

	if myConn == nil {
		return
	}

	for {
		l.wsMu.RLock()
		currentConn := l.ws
		l.wsMu.RUnlock()
		if currentConn != myConn {
			return
		}

		_, msg, err := myConn.ReadMessage()
		if err == nil {
			l.handleLLEvent(msg)
			continue
		}

		l.wsMu.Lock()
		if l.ws == myConn {
			l.ws = nil
			l.llSession = ""
		}
		l.wsMu.Unlock()

		_ = myConn.Close()
		log.Printf("[Lavalink] WS disconnected: %v", err)

		for attempt := 1; attempt <= 10; attempt++ {
			time.Sleep(time.Duration(attempt) * time.Second)
			if err := l.connectLLWS(); err == nil {
				return
			}
			log.Printf("[Lavalink] reconnect attempt %d failed", attempt)
		}
		log.Printf("[Lavalink] gave up reconnecting after 10 attempts")
		return
	}
}

func (l *LavalinkBackend) handleLLEvent(msg []byte) {
	var ev struct {
		Op      string `json:"op"`
		Type    string `json:"type"`
		GuildID string `json:"guildId"`
		Track   *struct {
			Info struct {
				Title string `json:"title"`
				URI   string `json:"uri"`
			} `json:"info"`
		} `json:"track"`
		Exception *struct {
			Message  string `json:"message"`
			Severity string `json:"severity"`
			Cause    string `json:"cause"`
		} `json:"exception"`
		Reason      string `json:"reason"`
		ThresholdMs int64  `json:"thresholdMs"`
		Code        int    `json:"code"`
		ByRemote    bool   `json:"byRemote"`
	}
	if err := json.Unmarshal(msg, &ev); err != nil {
		return
	}

	switch ev.Op {
	case "event":
		trackTitle := "(unknown)"
		if ev.Track != nil {
			trackTitle = ev.Track.Info.Title
		}

		switch ev.Type {
		case "TrackStartEvent":
			log.Printf("[Lavalink] TrackStart: %s (guild=%s)", trackTitle, ev.GuildID)

		case "TrackEndEvent":
			log.Printf("[Lavalink] TrackEnd: %s reason=%s (guild=%s)", trackTitle, ev.Reason, ev.GuildID)

		case "TrackExceptionEvent":
			excMsg := "(no details)"
			excSev := ""
			excCause := ""
			if ev.Exception != nil {
				excMsg = ev.Exception.Message
				excSev = ev.Exception.Severity
				excCause = ev.Exception.Cause
			}
			log.Printf("[Lavalink] TrackException: %s — %s (severity=%s, cause=%s, guild=%s)",
				trackTitle, excMsg, excSev, excCause, ev.GuildID)

		case "TrackStuckEvent":
			log.Printf("[Lavalink] TrackStuck: %s threshold=%dms (guild=%s)",
				trackTitle, ev.ThresholdMs, ev.GuildID)

		case "WebSocketClosedEvent":
			log.Printf("[Lavalink] WebSocketClosed: code=%d byRemote=%v (guild=%s)",
				ev.Code, ev.ByRemote, ev.GuildID)

		default:
			log.Printf("[Lavalink] event: type=%s guild=%s", ev.Type, ev.GuildID)
		}

	case "stats":
	default:
		if ev.Op != "ready" && ev.Op != "playerUpdate" {
			log.Printf("[Lavalink] ws op=%s", ev.Op)
		}
	}
}

func (l *LavalinkBackend) getLLSession() string {
	l.wsMu.RLock()
	defer l.wsMu.RUnlock()
	return l.llSession
}

func (l *LavalinkBackend) ensureLLSession() (string, error) {
	if sid := l.getLLSession(); sid != "" {
		return sid, nil
	}
	if err := l.connectLLWS(); err != nil {
		return "", err
	}
	if sid := l.getLLSession(); sid != "" {
		return sid, nil
	}
	return "", fmt.Errorf("no lavalink sessionId after reconnect")
}

func isHTTP(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func (l *LavalinkBackend) ResolveSong(query string) (*Song, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}

	identifier := q
	if !isHTTP(q) {
		identifier = "ytsearch:" + q
	}

	u := fmt.Sprintf("%s/v4/loadtracks?identifier=%s", l.baseURL(), url.QueryEscape(identifier))
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", l.password)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lavalink loadtracks: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		LoadType string          `json:"loadType"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("lavalink parse: %w", err)
	}

	switch result.LoadType {
	case "track":
		var track lavalinkTrack
		_ = json.Unmarshal(result.Data, &track)
		return trackToSong(&track), nil

	case "search":
		var tracks []lavalinkTrack
		_ = json.Unmarshal(result.Data, &tracks)
		if len(tracks) == 0 {
			return nil, fmt.Errorf("no results found for: %s", query)
		}
		return trackToSong(&tracks[0]), nil

	case "playlist":
		var playlist struct {
			Info   struct{ Name string } `json:"info"`
			Tracks []lavalinkTrack       `json:"tracks"`
		}
		_ = json.Unmarshal(result.Data, &playlist)
		if len(playlist.Tracks) == 0 {
			return nil, fmt.Errorf("empty playlist")
		}
		return trackToSong(&playlist.Tracks[0]), nil

	case "empty":
		return nil, fmt.Errorf("no results found for: %s", query)

	case "error":
		return nil, fmt.Errorf("lavalink error loading track")

	default:
		return nil, fmt.Errorf("unknown loadType: %s", result.LoadType)
	}
}

type lavalinkTrack struct {
	Encoded string `json:"encoded"`
	Info    struct {
		Title  string `json:"title"`
		Length int64  `json:"length"`
		URI    string `json:"uri"`
	} `json:"info"`
}

func trackToSong(t *lavalinkTrack) *Song {
	return &Song{
		Title:     t.Info.Title,
		URL:       t.Info.URI,
		StreamURL: t.Encoded,
		Duration:  int(t.Info.Length / 1000),
	}
}

func waitVoiceReady(guildID string, timeout time.Duration) (token, endpoint, sessionID string, err error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tok, endp, sid, ok := getVoiceInfo(guildID)
		if ok {
			return tok, endp, sid, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	tok, endp, sid, _ := getVoiceInfo(guildID)
	return tok, endp, sid, fmt.Errorf("voice not ready (missing token/endpoint/sessionId)")
}

func (l *LavalinkBackend) updateVoice(llSessionID, guildID string) error {
	token, endpoint, voiceSessionID, err := waitVoiceReady(guildID, 8*time.Second)
	if err != nil {
		return err
	}

	log.Printf("[Lavalink] voice info captured: token=%s..., endpoint=%s, sessionId=%s", truncate(token, 8), endpoint, truncate(voiceSessionID, 12))

	err = l.patchVoice(llSessionID, guildID, token, endpoint, voiceSessionID)
	if err == nil {
		return nil
	}

	if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "Session not found") {
		log.Printf("[Lavalink] session stale, reconnecting WS and retrying voice update...")
		if reconnErr := l.connectLLWS(); reconnErr != nil {
			return fmt.Errorf("voice update failed (%v) and reconnect failed: %w", err, reconnErr)
		}
		newSID := l.getLLSession()
		if newSID == "" {
			return fmt.Errorf("voice update failed (%v) and no new session after reconnect", err)
		}
		log.Printf("[Lavalink] reconnected, new sessionId=%s. Retrying voice update...", newSID)

		token2, endpoint2, voiceSID2, err2 := waitVoiceReady(guildID, 5*time.Second)
		if err2 == nil {
			token, endpoint, voiceSessionID = token2, endpoint2, voiceSID2
		}

		return l.patchVoice(newSID, guildID, token, endpoint, voiceSessionID)
	}

	return err
}

func (l *LavalinkBackend) patchVoice(llSessionID, guildID, token, endpoint, voiceSessionID string) error {
	playerURL := fmt.Sprintf("%s/v4/sessions/%s/players/%s", l.baseURL(), llSessionID, guildID)

	payload := map[string]any{
		"voice": map[string]any{
			"token":     token,
			"endpoint":  endpoint,
			"sessionId": voiceSessionID,
		},
	}

	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest("PATCH", playerURL, bytes.NewReader(b))
	req.Header.Set("Authorization", l.password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("voice update failed: %d %s", resp.StatusCode, string(body))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (l *LavalinkBackend) Play(vc *discordgo.VoiceConnection, song *Song, volume int, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	l.mu.Lock()
	l.stopFlag = false
	l.volume = volume
	l.currentGuildID = vc.GuildID
	l.mu.Unlock()

	llSessionID, err := l.ensureLLSession()
	if err != nil {
		log.Printf("[Lavalink] ws session error: %v", err)
		return
	}

	if err := l.updateVoice(llSessionID, vc.GuildID); err != nil {
		log.Printf("[Lavalink] voice update error: %v", err)
		return
	}

	llSessionID = l.getLLSession()
	if llSessionID == "" {
		log.Printf("[Lavalink] no session after voice update")
		return
	}

	playerURL := fmt.Sprintf("%s/v4/sessions/%s/players/%s", l.baseURL(), llSessionID, vc.GuildID)

	startPayload := map[string]any{
		"track":  map[string]any{"encoded": song.StreamURL},
		"volume": volume,
		"paused": false,
	}
	startJSON, _ := json.Marshal(startPayload)

	req, _ := http.NewRequest("PATCH", playerURL, bytes.NewReader(startJSON))
	req.Header.Set("Authorization", l.password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Lavalink] player start error: %v", err)
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Lavalink] player start returned %d: %s", resp.StatusCode, string(body))
		_ = resp.Body.Close()
		return
	}
	_ = resp.Body.Close()

	log.Printf("[Lavalink] started track: %s (session=%s, guild=%s)", song.Title, llSessionID, vc.GuildID)

	time.Sleep(3 * time.Second)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		l.mu.Lock()
		stopped := l.stopFlag
		l.mu.Unlock()

		if stopped {
			curSID := l.getLLSession()
			if curSID != "" {
				_ = l.destroyPlayer(curSID, vc.GuildID)
			}
			return
		}

		curSID := l.getLLSession()
		if curSID == "" {
			log.Printf("[Lavalink] lost session during playback, stopping")
			return
		}

		playing, err := l.isPlaying(curSID, vc.GuildID)
		if err != nil {
			log.Printf("[Lavalink] isPlaying error: %v", err)
			return
		}
		if !playing {
			log.Printf("[Lavalink] track finished for guild %s", vc.GuildID)
			return
		}
	}
}

func (l *LavalinkBackend) isPlaying(llSessionID, guildID string) (bool, error) {
	u := fmt.Sprintf("%s/v4/sessions/%s/players/%s", l.baseURL(), llSessionID, guildID)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", l.password)

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		log.Printf("[Lavalink] isPlaying: player not found (404) for guild %s", guildID)
		return false, nil
	}

	body, _ := io.ReadAll(resp.Body)

	var player struct {
		Track *json.RawMessage `json:"track"`
		State struct {
			Position  int64 `json:"position"`
			Connected bool  `json:"connected"`
		} `json:"state"`
	}
	_ = json.Unmarshal(body, &player)

	hasTrack := player.Track != nil && string(*player.Track) != "null"
	if !hasTrack {
		log.Printf("[Lavalink] isPlaying: no track for guild %s (connected=%v)", guildID, player.State.Connected)
	}

	return hasTrack, nil
}

func (l *LavalinkBackend) destroyPlayer(llSessionID, guildID string) error {
	u := fmt.Sprintf("%s/v4/sessions/%s/players/%s", l.baseURL(), llSessionID, guildID)
	req, _ := http.NewRequest("DELETE", u, nil)
	req.Header.Set("Authorization", l.password)

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (l *LavalinkBackend) Stop() {
	l.mu.Lock()
	l.stopFlag = true
	l.mu.Unlock()
}

func (l *LavalinkBackend) SetVolume(vol int) {
	l.mu.Lock()
	l.volume = vol
	guildID := l.currentGuildID
	l.mu.Unlock()

	if guildID == "" {
		return
	}

	llSessionID, err := l.ensureLLSession()
	if err != nil {
		return
	}

	u := fmt.Sprintf("%s/v4/sessions/%s/players/%s", l.baseURL(), llSessionID, guildID)

	payload, _ := json.Marshal(map[string]any{"volume": vol})
	req, _ := http.NewRequest("PATCH", u, bytes.NewReader(payload))
	req.Header.Set("Authorization", l.password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func (l *LavalinkBackend) SetPaused(paused bool) {
	l.mu.Lock()
	guildID := l.currentGuildID
	l.mu.Unlock()

	if guildID == "" {
		return
	}

	llSessionID, err := l.ensureLLSession()
	if err != nil {
		return
	}

	u := fmt.Sprintf("%s/v4/sessions/%s/players/%s", l.baseURL(), llSessionID, guildID)

	payload, _ := json.Marshal(map[string]any{"paused": paused})
	req, _ := http.NewRequest("PATCH", u, bytes.NewReader(payload))
	req.Header.Set("Authorization", l.password)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func (l *LavalinkBackend) Cleanup() {
	l.Stop()
	l.wsMu.Lock()
	if l.ws != nil {
		_ = l.ws.Close()
		l.ws = nil
	}
	l.llSession = ""
	l.wsMu.Unlock()
}
