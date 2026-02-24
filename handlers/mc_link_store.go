package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"discord-bot/storage"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MCLink struct {
	DiscordID string `json:"discord_id" bson:"discord_id"`
	UUID      string `json:"uuid"       bson:"uuid"`
	Username  string `json:"username"   bson:"username"`
	LinkedAt  string `json:"linked_at"  bson:"linked_at"`
}

type MCLinkConfirmation struct {
	Code      string
	DiscordID string
	UUID      string
	Username  string
}

type MCLinkStore interface {
	SavePendingCode(code, discordID, guildID string, expiresAt time.Time) error

	PopConfirmed() ([]MCLinkConfirmation, error)

	SaveLink(link MCLink) error

	LoadLink(discordID string) (*MCLink, error)

	DeleteLink(discordID string) error

	ListLinks() ([]MCLink, error)
}

var MCStore MCLinkStore

func InitMCLinkStore() {
	backend := storage.Cfg.Minecraft.LinkBackend
	if backend == "" {
		backend = "file"
	}

	switch backend {
	case "mongodb":
		s, err := newMongoLinkStore()
		if err != nil {
			log.Fatalf("[MC] Failed to initialise MongoDB link store: %v", err)
		}
		MCStore = s
		log.Println("[MC] Link backend: mongodb")
	default:
		MCStore = newFileLinkStore()
		log.Println("[MC] Link backend: file")
	}
}

const (
	mcLinkDir        = "data/mc_links"
	mcLinkPendingDir = "data/mc_links/pending"
)

type fileLinkStore struct {
	pending map[string]filePendingEntry
}

type filePendingEntry struct {
	DiscordID string    `json:"discord_id"`
	GuildID   string    `json:"guild_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func newFileLinkStore() *fileLinkStore {
	_ = os.MkdirAll(mcLinkDir, 0755)
	_ = os.MkdirAll(mcLinkPendingDir, 0755)
	return &fileLinkStore{pending: make(map[string]filePendingEntry)}
}

func (f *fileLinkStore) SavePendingCode(code, discordID, guildID string, expiresAt time.Time) error {
	entry := filePendingEntry{
		DiscordID: discordID,
		GuildID:   guildID,
		ExpiresAt: expiresAt,
	}
	pendingLinksMu.Lock()
	pendingLinks[code] = pendingLink{discordID: discordID, expiresAt: expiresAt}
	pendingLinksMu.Unlock()

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fmt.Sprintf("%s/%s.json", mcLinkPendingDir, code), data, 0644)
}

func (f *fileLinkStore) PopConfirmed() ([]MCLinkConfirmation, error) {
	entries, err := os.ReadDir(mcLinkPendingDir)
	if err != nil {
		return nil, nil
	}

	var results []MCLinkConfirmation
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(e.Name(), ".json")
		filePath := mcLinkPendingDir + "/" + e.Name()

		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var payload struct {
			UUID      string `json:"uuid"`
			Username  string `json:"username"`
			Confirmed bool   `json:"confirmed"`
		}
		if err := json.Unmarshal(data, &payload); err != nil || !payload.Confirmed {
			pendingLinksMu.Lock()
			p, ok := pendingLinks[code]
			if ok && time.Now().After(p.expiresAt) {
				delete(pendingLinks, code)
				_ = os.Remove(filePath)
			}
			pendingLinksMu.Unlock()
			continue
		}

		discordID, valid := ConsumeLinkCode(code)
		if !valid {
			_ = os.Remove(filePath)
			continue
		}

		_ = os.Remove(filePath)
		results = append(results, MCLinkConfirmation{
			Code:      code,
			DiscordID: discordID,
			UUID:      payload.UUID,
			Username:  payload.Username,
		})
	}
	return results, nil
}

func (f *fileLinkStore) SaveLink(link MCLink) error {
	_ = os.MkdirAll(mcLinkDir, 0755)
	data, err := json.MarshalIndent(link, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fmt.Sprintf("%s/%s.json", mcLinkDir, link.DiscordID), data, 0644)
}

func (f *fileLinkStore) LoadLink(discordID string) (*MCLink, error) {
	data, err := os.ReadFile(fmt.Sprintf("%s/%s.json", mcLinkDir, discordID))
	if err != nil {
		return nil, err
	}
	var link MCLink
	if err := json.Unmarshal(data, &link); err != nil {
		return nil, err
	}
	return &link, nil
}

func (f *fileLinkStore) DeleteLink(discordID string) error {
	return os.Remove(fmt.Sprintf("%s/%s.json", mcLinkDir, discordID))
}

func (f *fileLinkStore) ListLinks() ([]MCLink, error) {
	entries, err := os.ReadDir(mcLinkDir)
	if err != nil {
		return nil, err
	}
	var links []MCLink
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		discordID := strings.TrimSuffix(e.Name(), ".json")
		link, err := f.LoadLink(discordID)
		if err != nil {
			continue
		}
		links = append(links, *link)
	}
	return links, nil
}

type mongoLinkStore struct {
	pending   *mongo.Collection
	confirmed *mongo.Collection
	links     *mongo.Collection
}

func newMongoLinkStore() (*mongoLinkStore, error) {
	uri := storage.Cfg.Database.MongoDB.URI
	db := storage.Cfg.Database.MongoDB.Database
	if uri == "" || db == "" {
		return nil, fmt.Errorf("database.mongodb.uri and database.mongodb.database must be set in config.json to use link_backend=mongodb")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	mdb := client.Database(db)
	store := &mongoLinkStore{
		pending:   mdb.Collection("discord_link_pending"),
		confirmed: mdb.Collection("discord_link_confirmed"),
		links:     mdb.Collection("discord_links"),
	}

	store.pending.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "code", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	store.pending.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	store.links.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "discord_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	store.links.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "uuid", Value: 1}},
	})

	return store, nil
}

func (m *mongoLinkStore) SavePendingCode(code, discordID, guildID string, expiresAt time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = m.pending.DeleteMany(ctx, bson.M{"discord_id": discordID})

	_, err := m.pending.InsertOne(ctx, bson.M{
		"code":       code,
		"discord_id": discordID,
		"guild_id":   guildID,
		"expires_at": expiresAt,
		"created_at": time.Now(),
	})
	return err
}

func (m *mongoLinkStore) PopConfirmed() ([]MCLinkConfirmation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cursor, err := m.confirmed.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []MCLinkConfirmation
	var processedIDs []interface{}

	for cursor.Next(ctx) {
		var doc struct {
			ID        interface{} `bson:"_id"`
			Code      string      `bson:"code"`
			DiscordID string      `bson:"discord_id"`
			UUID      string      `bson:"uuid"`
			Username  string      `bson:"username"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		results = append(results, MCLinkConfirmation{
			Code:      doc.Code,
			DiscordID: doc.DiscordID,
			UUID:      doc.UUID,
			Username:  doc.Username,
		})
		processedIDs = append(processedIDs, doc.ID)
	}

	if len(processedIDs) > 0 {
		_, _ = m.confirmed.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": processedIDs}})
	}

	return results, nil
}

func (m *mongoLinkStore) SaveLink(link MCLink) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := m.links.ReplaceOne(
		ctx,
		bson.M{"discord_id": link.DiscordID},
		link,
		options.Replace().SetUpsert(true),
	)
	return err
}

func (m *mongoLinkStore) LoadLink(discordID string) (*MCLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var link MCLink
	err := m.links.FindOne(ctx, bson.M{"discord_id": discordID}).Decode(&link)
	if err == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("not linked")
	}
	return &link, err
}

func (m *mongoLinkStore) DeleteLink(discordID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := m.links.DeleteOne(ctx, bson.M{"discord_id": discordID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}

func (m *mongoLinkStore) ListLinks() ([]MCLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cursor, err := m.links.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var links []MCLink
	return links, cursor.All(ctx, &links)
}
