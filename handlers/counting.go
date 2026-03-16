package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"discord-bot/config"

	"github.com/bwmarrin/discordgo"
)

// ── State ─────────────────────────────────────────────────────────────────────

type countingState struct {
	mu sync.Mutex

	Count      int    `json:"count"`
	LastUserID string `json:"last_user_id"`
	HighScore  int    `json:"high_score"`
}

var counting = &countingState{}

const countingStatePath = "data/counting.json"

func loadCountingState() {
	data, err := os.ReadFile(countingStatePath)
	if err != nil {
		return // first run, start from zero
	}
	counting.mu.Lock()
	defer counting.mu.Unlock()
	_ = json.Unmarshal(data, counting)
}

func saveCountingState() {
	// caller must NOT hold the lock — we acquire read-only snapshot first
	counting.mu.Lock()
	snapshot, _ := json.MarshalIndent(counting, "", "  ")
	counting.mu.Unlock()

	_ = os.MkdirAll("data", 0755)
	if err := os.WriteFile(countingStatePath, snapshot, 0644); err != nil {
		log.Printf("[Counting] Failed to save state: %v", err)
	}
}

// ── Registration ──────────────────────────────────────────────────────────────

func RegisterCounting(s *discordgo.Session, cfg *config.Config) {
	if !cfg.CountingGame.Enabled || cfg.CountingGame.ChannelID == "" {
		return
	}
	loadCountingState()
	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		handleCountingMessage(s, m, &cfg.CountingGame)
	})
	log.Printf("[Counting] Game active in channel %s (current count: %d, high score: %d)",
		cfg.CountingGame.ChannelID, counting.Count, counting.HighScore)
}

// ── Handler ───────────────────────────────────────────────────────────────────

func handleCountingMessage(s *discordgo.Session, m *discordgo.MessageCreate, cfg *config.CountingGameConfig) {
	// Ignore bots and messages outside the counting channel.
	if m.Author.Bot || m.ChannelID != cfg.ChannelID {
		return
	}

	content := strings.TrimSpace(m.Content)

	// Check if the message is a number.
	number, err := strconv.Atoi(content)
	if err != nil {
		// Not a number.
		if cfg.DeleteNonNumbers {
			_ = s.ChannelMessageDelete(m.ChannelID, m.ID)
			sendTemp(s, m.ChannelID, fmt.Sprintf("<@%s> Only numbers allowed in this channel!", m.Author.ID), 5)
		}
		return
	}

	counting.mu.Lock()
	expected := counting.Count + 1
	lastUser := counting.LastUserID
	currentCount := counting.Count
	highScore := counting.HighScore
	counting.mu.Unlock()

	// ── Same player check ──────────────────────────────────────────────────────
	if m.Author.ID == lastUser {
		if cfg.DeleteWrong {
			_ = s.ChannelMessageDelete(m.ChannelID, m.ID)
		}
		sendTemp(s, m.ChannelID,
			fmt.Sprintf("<@%s> You wrote the last number! Wait for someone else to count before you go again.", m.Author.ID),
			7,
		)
		return
	}

	// ── Wrong number check ─────────────────────────────────────────────────────
	if number != expected {
		if cfg.DeleteWrong {
			_ = s.ChannelMessageDelete(m.ChannelID, m.ID)
		}

		if cfg.FailResets {
			// Save the high score before resetting.
			newHigh := highScore
			if currentCount > highScore {
				newHigh = currentCount
			}
			counting.mu.Lock()
			counting.Count = 0
			counting.LastUserID = ""
			if newHigh > counting.HighScore {
				counting.HighScore = newHigh
			}
			counting.mu.Unlock()
			go saveCountingState()

			msg := fmt.Sprintf(
				"<@%s> wrote **%d** but the next number was **%d**. The count resets to **0**!\n"+
					"Count got to **%d**. High score: **%d**. Start again from **1**!",
				m.Author.ID, number, expected, currentCount, newHigh,
			)
			_, _ = s.ChannelMessageSend(m.ChannelID, msg)
		} else {
			sendTemp(s, m.ChannelID,
				fmt.Sprintf("<@%s> Wrong number! The next number is **%d**, not %d.", m.Author.ID, expected, number),
				7,
			)
		}
		return
	}

	// ── Correct number ─────────────────────────────────────────────────────────
	counting.mu.Lock()
	counting.Count = number
	counting.LastUserID = m.Author.ID
	newHigh := counting.HighScore
	if number > counting.HighScore {
		counting.HighScore = number
		newHigh = number
	}
	counting.mu.Unlock()
	go saveCountingState()

	// React with a checkmark so the channel stays clean.
	_ = s.MessageReactionAdd(m.ChannelID, m.ID, "✅")

	// Celebrate milestones.
	if number%100 == 0 {
		_, _ = s.ChannelMessageSend(m.ChannelID,
			fmt.Sprintf("🎉 **%d!** Amazing counting everyone!", number),
		)
	} else if number == newHigh && number > 1 {
		// New high score (only announce on improvements, not every number).
		if number%10 == 0 {
			_, _ = s.ChannelMessageSend(m.ChannelID,
				fmt.Sprintf("📈 New high score: **%d**!", number),
			)
		}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// sendTemp sends a message to a channel and deletes it after `seconds`.
func sendTemp(s *discordgo.Session, channelID, content string, seconds int) {
	msg, err := s.ChannelMessageSend(channelID, content)
	if err != nil {
		return
	}
	go func() {
		time.Sleep(time.Duration(seconds) * time.Second)
		_ = s.ChannelMessageDelete(channelID, msg.ID)
	}()
}
