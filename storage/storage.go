package storage

import (
	"discord-bot/config"
	"sync"
)

var Cfg *config.Config

var (
	guilds = make(map[string]*config.GuildState)
	mu     sync.RWMutex
)

func GetGuild(guildID string) *config.GuildState {
	mu.RLock()
	gs, ok := guilds[guildID]
	mu.RUnlock()
	if ok {
		return gs
	}

	mu.Lock()
	defer mu.Unlock()
	if gs, ok = guilds[guildID]; ok {
		return gs
	}
	gs = config.LoadGuildState(guildID)
	guilds[guildID] = gs
	return gs
}
