package lang

import (
	"log"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	mu       sync.RWMutex
	messages map[string]string
)

func Load(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[lang] Could not read %s: %v — using empty translations", path, err)
		mu.Lock()
		messages = make(map[string]string)
		mu.Unlock()
		return
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		log.Fatalf("[lang] Failed to parse %s: %v", path, err)
	}

	activeLang := "en"
	if v, ok := raw["active_language"]; ok {
		if s, ok := v.(string); ok && s != "" {
			activeLang = s
		}
	}

	block, ok := raw[activeLang]
	if !ok {
		log.Printf("[lang] Language %q not found in %s — falling back to \"en\"", activeLang, path)
		activeLang = "en"
		block, ok = raw[activeLang]
		if !ok {
			log.Printf("[lang] Fallback \"en\" also missing — using empty translations")
			mu.Lock()
			messages = make(map[string]string)
			mu.Unlock()
			return
		}
	}

	blockMap, ok := block.(map[string]interface{})
	if !ok {
		log.Fatalf("[lang] Language block %q is not a map", activeLang)
	}

	m := make(map[string]string, len(blockMap))
	for k, v := range blockMap {
		if s, ok := v.(string); ok {
			m[k] = s
		}
	}

	mu.Lock()
	messages = m
	mu.Unlock()

	log.Printf("[lang] Loaded language %q (%d keys)", activeLang, len(m))
}

func T(key string, pairs ...string) string {
	mu.RLock()
	s, ok := messages[key]
	mu.RUnlock()

	if !ok {
		return "{" + key + "}"
	}

	if len(pairs) == 0 {
		return s
	}

	for j := 0; j+1 < len(pairs); j += 2 {
		s = strings.ReplaceAll(s, "{"+pairs[j]+"}", pairs[j+1])
	}
	return s
}

func Reload(path string) {
	Load(path)
}
