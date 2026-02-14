package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"discord-bot/config"
)

var DB Database

type Database interface {
	Init() error
	Close() error

	AddWarning(guildID, userID string, w config.Warning) error
	GetWarnings(guildID, userID string) ([]config.Warning, error)
	ClearWarnings(guildID, userID string) error

	AddModCase(guildID string, c ModCase) error
	GetModCases(guildID, userID string, limit int) ([]ModCase, error)
}

type ModCase struct {
	ID        int    `json:"id"`
	GuildID   string `json:"guild_id"`
	UserID    string `json:"user_id"`
	ModID     string `json:"mod_id"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	Duration  string `json:"duration,omitempty"`
	Timestamp string `json:"timestamp"`
}

func InitDB(cfg *config.DatabaseConfig) error {
	switch cfg.Driver {
	case "sqlite":
		db := &SQLiteDB{Path: cfg.SQLite.Path}
		if err := db.Init(); err != nil {
			return err
		}
		DB = db
		return nil

	case "mongodb":
		db := &MongoDB{URI: cfg.MongoDB.URI, DBName: cfg.MongoDB.Database}
		if err := db.Init(); err != nil {
			return err
		}
		DB = db
		return nil

	default:
		return fmt.Errorf("unsupported database driver: %s (use \"sqlite\" or \"mongodb\")", cfg.Driver)
	}
}

type SQLiteDB struct {
	Path string
	db   *sql.DB
}

func (s *SQLiteDB) Init() error {
	_ = os.MkdirAll(filepath.Dir(s.Path), 0755)

	db, err := sql.Open("sqlite", s.Path)
	if err != nil {
		return fmt.Errorf("sqlite open: %w", err)
	}
	s.db = db

	schema := `
	CREATE TABLE IF NOT EXISTS warnings (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		guild_id    TEXT NOT NULL,
		user_id     TEXT NOT NULL,
		mod_id      TEXT NOT NULL,
		reason      TEXT NOT NULL,
		timestamp   TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_warnings_guild_user ON warnings(guild_id, user_id);

	CREATE TABLE IF NOT EXISTS mod_cases (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		guild_id    TEXT NOT NULL,
		user_id     TEXT NOT NULL,
		mod_id      TEXT NOT NULL,
		action      TEXT NOT NULL,
		reason      TEXT NOT NULL DEFAULT '',
		duration    TEXT NOT NULL DEFAULT '',
		timestamp   TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_mod_cases_guild_user ON mod_cases(guild_id, user_id);
	`
	_, err = db.Exec(schema)
	if err != nil {
		return fmt.Errorf("sqlite schema: %w", err)
	}
	log.Printf("[DB] SQLite initialised at %s", s.Path)
	return nil
}

func (s *SQLiteDB) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteDB) AddWarning(guildID, userID string, w config.Warning) error {
	_, err := s.db.Exec(
		"INSERT INTO warnings (guild_id, user_id, mod_id, reason, timestamp) VALUES (?, ?, ?, ?, ?)",
		guildID, userID, w.ModID, w.Reason, w.Timestamp,
	)
	return err
}

func (s *SQLiteDB) GetWarnings(guildID, userID string) ([]config.Warning, error) {
	rows, err := s.db.Query(
		"SELECT id, mod_id, reason, timestamp FROM warnings WHERE guild_id = ? AND user_id = ? ORDER BY id",
		guildID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var warns []config.Warning
	for rows.Next() {
		var w config.Warning
		if err := rows.Scan(&w.ID, &w.ModID, &w.Reason, &w.Timestamp); err != nil {
			continue
		}
		warns = append(warns, w)
	}
	return warns, nil
}

func (s *SQLiteDB) ClearWarnings(guildID, userID string) error {
	_, err := s.db.Exec("DELETE FROM warnings WHERE guild_id = ? AND user_id = ?", guildID, userID)
	return err
}

func (s *SQLiteDB) AddModCase(guildID string, c ModCase) error {
	_, err := s.db.Exec(
		"INSERT INTO mod_cases (guild_id, user_id, mod_id, action, reason, duration, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)",
		guildID, c.UserID, c.ModID, c.Action, c.Reason, c.Duration, c.Timestamp,
	)
	return err
}

func (s *SQLiteDB) GetModCases(guildID, userID string, limit int) ([]ModCase, error) {
	rows, err := s.db.Query(
		"SELECT id, guild_id, user_id, mod_id, action, reason, duration, timestamp FROM mod_cases WHERE guild_id = ? AND user_id = ? ORDER BY id DESC LIMIT ?",
		guildID, userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cases []ModCase
	for rows.Next() {
		var c ModCase
		if err := rows.Scan(&c.ID, &c.GuildID, &c.UserID, &c.ModID, &c.Action, &c.Reason, &c.Duration, &c.Timestamp); err != nil {
			continue
		}
		cases = append(cases, c)
	}
	return cases, nil
}

type MongoDB struct {
	URI    string
	DBName string
	dir    string
}

func (m *MongoDB) Init() error {

	m.dir = "data/mongodb_fallback"
	_ = os.MkdirAll(m.dir, 0755)
	log.Printf("[DB] MongoDB fallback (JSON files) initialised at %s", m.dir)
	log.Println("[DB] For production, replace storage/database.go MongoDB section with the official mongo-driver.")
	return nil
}

func (m *MongoDB) Close() error { return nil }

func (m *MongoDB) collectionPath(name string) string {
	return filepath.Join(m.dir, name+".json")
}

func (m *MongoDB) loadCollection(name string, v interface{}) error {
	data, err := os.ReadFile(m.collectionPath(name))
	if err != nil {
		return nil
	}
	return json.Unmarshal(data, v)
}

func (m *MongoDB) saveCollection(name string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.collectionPath(name), data, 0644)
}

func (m *MongoDB) AddWarning(guildID, userID string, w config.Warning) error {
	var warns []config.Warning
	_ = m.loadCollection("warnings_"+guildID+"_"+userID, &warns)
	w.ID = len(warns) + 1
	warns = append(warns, w)
	return m.saveCollection("warnings_"+guildID+"_"+userID, &warns)
}

func (m *MongoDB) GetWarnings(guildID, userID string) ([]config.Warning, error) {
	var warns []config.Warning
	_ = m.loadCollection("warnings_"+guildID+"_"+userID, &warns)
	return warns, nil
}

func (m *MongoDB) ClearWarnings(guildID, userID string) error {
	path := m.collectionPath("warnings_" + guildID + "_" + userID)
	return os.Remove(path)
}

func (m *MongoDB) AddModCase(guildID string, c ModCase) error {
	var cases []ModCase
	_ = m.loadCollection("modcases_"+guildID, &cases)
	c.ID = len(cases) + 1
	cases = append(cases, c)
	return m.saveCollection("modcases_"+guildID, &cases)
}

func (m *MongoDB) GetModCases(guildID, userID string, limit int) ([]ModCase, error) {
	var all []ModCase
	_ = m.loadCollection("modcases_"+guildID, &all)

	var filtered []ModCase
	for i := len(all) - 1; i >= 0 && len(filtered) < limit; i-- {
		if all[i].UserID == userID {
			filtered = append(filtered, all[i])
		}
	}
	return filtered, nil
}
