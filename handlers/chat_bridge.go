package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"discord-bot/config"

	"github.com/bwmarrin/discordgo"
	amqp "github.com/rabbitmq/amqp091-go"
)

// bridgeOriginID is used as the "server ID" field in RabbitMQ payloads sent from Discord.
// Minecraft servers compare it against their own UUID — since "DISCORD" is never a valid
// server UUID, no Minecraft server will filter it as a loopback message.
const bridgeOriginID = "DISCORD"

// ActiveBridge is the running ChatBridge instance, set in Start().
// Other handlers (moderation.go) use this to trigger MC sync actions.
var ActiveBridge *ChatBridge

// ChatBridge provides a bidirectional Minecraft ↔ Discord chat bridge over RabbitMQ.
// It uses two independent connections: one for publishing (Discord→MC) and one for
// consuming (MC→Discord), so a consumer error never breaks outgoing messages.
type ChatBridge struct {
	session *discordgo.Session
	cfg     *config.ChatBridgeConfig

	// publisher
	pubConn *amqp.Connection
	pubCh   *amqp.Channel

	// consumer
	subConn *amqp.Connection
	subCh   *amqp.Channel
}

// NewChatBridge creates the bridge and connects the publisher.
// Call Start() afterwards to register the Discord handler and begin consuming RabbitMQ.
func NewChatBridge(s *discordgo.Session, cfg *config.ChatBridgeConfig) (*ChatBridge, error) {
	b := &ChatBridge{session: s, cfg: cfg}
	if err := b.connectPublisher(); err != nil {
		return nil, err
	}
	return b, nil
}

// Start registers all Discord event handlers and launches the RabbitMQ consumer goroutine.
func (b *ChatBridge) Start() {
	ActiveBridge = b

	b.session.AddHandler(b.onDiscordMessage)

	if b.cfg.BanSync {
		b.session.AddHandler(b.onGuildBanAdd)
		b.session.AddHandler(b.onGuildBanRemove)
	}

	go b.consumeLoop()
	log.Println("[ChatBridge] Started — bridging Discord channel", b.cfg.ChannelID, "↔ Minecraft via RabbitMQ")
}

// Stop closes all AMQP connections.
func (b *ChatBridge) Stop() {
	for _, ch := range []*amqp.Channel{b.pubCh, b.subCh} {
		if ch != nil {
			_ = ch.Close()
		}
	}
	for _, conn := range []*amqp.Connection{b.pubConn, b.subConn} {
		if conn != nil {
			_ = conn.Close()
		}
	}
}

// ── Discord → Minecraft ──────────────────────────────────────────────────────

// onDiscordMessage is the discordgo event handler for new messages.
// It only processes messages in the configured channel from linked users.
func (b *ChatBridge) onDiscordMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bots, webhooks, and messages outside the bridge channel.
	if m.Author.Bot || m.WebhookID != "" || m.ChannelID != b.cfg.ChannelID {
		return
	}
	if MCStore == nil {
		return
	}

	link, err := MCStore.LoadLink(m.Author.ID)
	if err != nil || link == nil {
		// User is not linked — silently ignore.
		return
	}

	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	payload := b.buildMCPayload(link.UUID, link.Username, content)

	if err := b.publish(payload); err != nil {
		log.Printf("[ChatBridge] Publish error: %v — reconnecting", err)
		if err2 := b.connectPublisher(); err2 == nil {
			_ = b.publish(payload)
		}
	}
}

// buildMCPayload constructs the RabbitMQ message body in the format OreoEssentials expects.
//
//   - When MCChannelID is set (channels mode): CHANMSG format
//   - Otherwise (legacy mode): legacy chat format
//
// Both cases wrap the message in a minimal Adventure text component JSON so the Minecraft
// side can deserialize it correctly with GsonComponentSerializer.
func (b *ChatBridge) buildMCPayload(uuid, playerName, content string) string {
	jsonComp := fmt.Sprintf(`{"text":"[Discord] %s: %s"}`, escapeJSON(playerName), escapeJSON(content))

	if b.cfg.MCChannelID != "" {
		// CHANMSG;;originId;;uuid;;b64(server);;b64(name);;b64(channelId);;b64(jsonComp)
		return strings.Join([]string{
			"CHANMSG",
			bridgeOriginID,
			uuid,
			b64enc("Discord"),
			b64enc(playerName),
			b64enc(b.cfg.MCChannelID),
			b64enc(jsonComp),
		}, ";;")
	}

	// Legacy: originId;;uuid;;b64(name);;b64(jsonComp)
	return strings.Join([]string{
		bridgeOriginID,
		uuid,
		b64enc(playerName),
		b64enc(jsonComp),
	}, ";;")
}

func (b *ChatBridge) publish(payload string) error {
	return b.pubCh.Publish("chat_sync", "", false, false, amqp.Publishing{
		ContentType: "text/plain",
		Body:        []byte(payload),
	})
}

// ── Minecraft → Discord ──────────────────────────────────────────────────────

// consumeLoop keeps the subscriber alive, reconnecting on failures.
func (b *ChatBridge) consumeLoop() {
	for {
		if err := b.connectSubscriber(); err != nil {
			log.Printf("[ChatBridge] Sub connect error: %v — retrying in 5s", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if err := b.consume(); err != nil {
			log.Printf("[ChatBridge] Consumer closed: %v — reconnecting in 5s", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func (b *ChatBridge) consume() error {
	q, err := b.subCh.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return fmt.Errorf("queue declare: %w", err)
	}
	if err := b.subCh.QueueBind(q.Name, "", "chat_sync", false, nil); err != nil {
		return fmt.Errorf("queue bind: %w", err)
	}
	msgs, err := b.subCh.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	for msg := range msgs {
		b.handleMCMessage(string(msg.Body))
	}
	return fmt.Errorf("consumer channel closed")
}

// handleMCMessage parses an incoming RabbitMQ payload and forwards it to Discord.
func (b *ChatBridge) handleMCMessage(raw string) {
	// Skip control and system messages entirely.
	if strings.HasPrefix(raw, "CTRL;;") || strings.HasPrefix(raw, "CHANSYS;;") {
		return
	}

	var playerName, message string

	if strings.HasPrefix(raw, "CHANMSG;;") {
		// CHANMSG;;originId;;uuid;;b64(server);;b64(name);;b64(channelId);;b64(jsonComp)
		parts := strings.SplitN(raw, ";;", 7)
		if len(parts) < 7 {
			return
		}
		if parts[1] == bridgeOriginID {
			return // echo from ourselves
		}
		channelID := b64dec(parts[5])
		// If MCChannelID is configured, only relay messages from that channel.
		if b.cfg.MCChannelID != "" && !strings.EqualFold(channelID, b.cfg.MCChannelID) {
			return
		}
		playerName = b64dec(parts[4])
		message = adventureToPlain(b64dec(parts[6]))

	} else {
		// Legacy: originId;;uuid;;b64(name);;b64(jsonComp)[;;b64(plainPrefix);;b64(rawMessage)]
		// The last two fields are optional — added in the extended format.
		parts := strings.SplitN(raw, ";;", 6)
		if len(parts) < 4 {
			return
		}
		if parts[0] == bridgeOriginID {
			return // echo from ourselves
		}
		playerName = b64dec(parts[2])

		if len(parts) == 6 {
			// Extended format: use the clean prefix + raw message directly.
			prefix := strings.TrimSpace(b64dec(parts[4]))
			rawMsg := b64dec(parts[5])
			if prefix != "" {
				message = prefix + " " + rawMsg
			} else {
				message = rawMsg
			}
		} else {
			// Legacy 4-field format: fall back to parsing the Adventure JSON.
			message = adventureToPlain(b64dec(parts[3]))
		}
	}

	if playerName == "" || message == "" {
		return
	}

	// Look up if this Minecraft player has a linked Discord account.
	discordMention := ""
	if MCStore != nil {
		if links, err := MCStore.ListLinks(); err == nil {
			for _, l := range links {
				if l.Username == playerName {
					discordMention = fmt.Sprintf(" (<@%s>)", l.DiscordID)
					break
				}
			}
		}
	}

	text := fmt.Sprintf("**%s**%s: %s", playerName, discordMention, message)
	if _, err := b.session.ChannelMessageSend(b.cfg.ChannelID, text); err != nil {
		log.Printf("[ChatBridge] Discord send error: %v", err)
	}
}

// ── Ban sync (Discord → Minecraft) ───────────────────────────────────────────

// onGuildBanAdd fires when any user is banned from the Discord guild.
// If the banned user is linked to a Minecraft account, they are also banned
// in Minecraft via RCON: `ban <username> Banned from Discord`.
func (b *ChatBridge) onGuildBanAdd(s *discordgo.Session, e *discordgo.GuildBanAdd) {
	if MCStore == nil || RCONClient == nil {
		return
	}
	link, err := MCStore.LoadLink(e.User.ID)
	if err != nil || link == nil {
		return
	}
	cmd := fmt.Sprintf("ban %s Banned from Discord", link.Username)
	if _, err := RCONClient.Command(cmd); err != nil {
		log.Printf("[ChatBridge] RCON ban failed for %s: %v", link.Username, err)
	} else {
		log.Printf("[ChatBridge] Banned %s from Minecraft (Discord ban sync)", link.Username)
	}
}

// onGuildBanRemove fires when a Discord ban is lifted.
// Pardons the linked Minecraft player via RCON: `pardon <username>`.
func (b *ChatBridge) onGuildBanRemove(s *discordgo.Session, e *discordgo.GuildBanRemove) {
	if MCStore == nil || RCONClient == nil {
		return
	}
	link, err := MCStore.LoadLink(e.User.ID)
	if err != nil || link == nil {
		return
	}
	cmd := fmt.Sprintf("pardon %s", link.Username)
	if _, err := RCONClient.Command(cmd); err != nil {
		log.Printf("[ChatBridge] RCON pardon failed for %s: %v", link.Username, err)
	} else {
		log.Printf("[ChatBridge] Pardoned %s in Minecraft (Discord unban sync)", link.Username)
	}
}

// ── Mod sync (Discord /mute → Minecraft) ─────────────────────────────────────

// SyncMuteToMC publishes a CTRL;;MUTE to RabbitMQ for the linked Minecraft player.
// Called from handleMute in moderation.go when ModSync is enabled.
// All Minecraft servers will apply the mute via OreoEssentials MuteService.
func (b *ChatBridge) SyncMuteToMC(discordUserID string, until time.Time, reason, byUsername string) {
	if !b.cfg.ModSync || MCStore == nil {
		return
	}
	link, err := MCStore.LoadLink(discordUserID)
	if err != nil || link == nil {
		return
	}
	payload := fmt.Sprintf("CTRL;;MUTE;;%s;;%s;;%d;;%s;;%s",
		bridgeOriginID,
		link.UUID,
		until.UnixMilli(),
		b64enc(reason),
		b64enc(byUsername),
	)
	if err := b.publish(payload); err != nil {
		log.Printf("[ChatBridge] SyncMuteToMC publish error: %v", err)
	} else {
		log.Printf("[ChatBridge] Synced mute for %s to Minecraft (until %s)", link.Username, until.Format(time.RFC3339))
	}
}

// SyncUnmuteToMC publishes a CTRL;;UNMUTE to RabbitMQ for the linked Minecraft player.
// Called from handleUnmute in moderation.go when ModSync is enabled.
func (b *ChatBridge) SyncUnmuteToMC(discordUserID string) {
	if !b.cfg.ModSync || MCStore == nil {
		return
	}
	link, err := MCStore.LoadLink(discordUserID)
	if err != nil || link == nil {
		return
	}
	payload := fmt.Sprintf("CTRL;;UNMUTE;;%s;;%s", bridgeOriginID, link.UUID)
	if err := b.publish(payload); err != nil {
		log.Printf("[ChatBridge] SyncUnmuteToMC publish error: %v", err)
	} else {
		log.Printf("[ChatBridge] Synced unmute for %s to Minecraft", link.Username)
	}
}

// ── AMQP connection helpers ──────────────────────────────────────────────────

func (b *ChatBridge) connectPublisher() error {
	conn, err := amqp.Dial(b.cfg.RabbitMQURI)
	if err != nil {
		return fmt.Errorf("pub dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("pub channel: %w", err)
	}
	if err := ch.ExchangeDeclare("chat_sync", "fanout", false, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("exchange declare: %w", err)
	}
	b.pubConn = conn
	b.pubCh = ch
	return nil
}

func (b *ChatBridge) connectSubscriber() error {
	conn, err := amqp.Dial(b.cfg.RabbitMQURI)
	if err != nil {
		return fmt.Errorf("sub dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("sub channel: %w", err)
	}
	if err := ch.ExchangeDeclare("chat_sync", "fanout", false, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("exchange declare: %w", err)
	}
	b.subConn = conn
	b.subCh = ch
	return nil
}

// ── Utility ──────────────────────────────────────────────────────────────────

// adventureToPlain converts a GsonComponentSerializer JSON string to plain text.
// Falls back to color-stripped raw string if JSON parsing fails.
func adventureToPlain(jsonStr string) string {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return stripColors(jsonStr)
	}
	return extractAdventureText(obj)
}

// extractAdventureText recursively extracts "text" fields from an Adventure component map.
func extractAdventureText(obj map[string]interface{}) string {
	var sb strings.Builder
	if t, ok := obj["text"].(string); ok {
		sb.WriteString(t)
	}
	if extra, ok := obj["extra"].([]interface{}); ok {
		for _, e := range extra {
			if child, ok := e.(map[string]interface{}); ok {
				sb.WriteString(extractAdventureText(child))
			}
		}
	}
	return sb.String()
}

// stripColors removes Minecraft § color codes and MiniMessage tags from a string.
func stripColors(s string) string {
	var sb strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '§' {
			i++ // skip the color code character after §
			continue
		}
		if runes[i] == '<' {
			if end := strings.IndexRune(string(runes[i:]), '>'); end != -1 {
				i += end
				continue
			}
		}
		sb.WriteRune(runes[i])
	}
	return sb.String()
}

func b64enc(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func b64dec(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// escapeJSON escapes a string for safe embedding inside a JSON string literal.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}
