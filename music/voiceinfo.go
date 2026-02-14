package music

import (
	"sync"
	"time"
)

type voiceInfo struct {
	sessionID string
	token     string
	endpoint  string
	updatedAt time.Time
}

var (
	voiceMu  sync.RWMutex
	voiceMap = map[string]*voiceInfo{}
)

func updateVoiceSessionID(guildID, sessionID string) {
	voiceMu.Lock()
	defer voiceMu.Unlock()

	v := voiceMap[guildID]
	if v == nil {
		v = &voiceInfo{}
		voiceMap[guildID] = v
	}
	v.sessionID = sessionID
	v.updatedAt = time.Now()
}

func updateVoiceServer(guildID, token, endpoint string) {
	voiceMu.Lock()
	defer voiceMu.Unlock()

	v := voiceMap[guildID]
	if v == nil {
		v = &voiceInfo{}
		voiceMap[guildID] = v
	}
	v.token = token
	v.endpoint = endpoint
	v.updatedAt = time.Now()
}

func getVoiceInfo(guildID string) (token, endpoint, sessionID string, ok bool) {
	voiceMu.RLock()
	defer voiceMu.RUnlock()

	v := voiceMap[guildID]
	if v == nil {
		return "", "", "", false
	}
	if v.token == "" || v.endpoint == "" || v.sessionID == "" {
		return v.token, v.endpoint, v.sessionID, false
	}
	return v.token, v.endpoint, v.sessionID, true
}

func UpdateVoiceState(guildID, sessionID string) {
	updateVoiceSessionID(guildID, sessionID)
}

func UpdateVoiceServer(guildID, token, endpoint string) {
	updateVoiceServer(guildID, token, endpoint)
}

func ClearVoiceInfo(guildID string) {
	voiceMu.Lock()
	defer voiceMu.Unlock()
	delete(voiceMap, guildID)
}
