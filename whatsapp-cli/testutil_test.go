package main

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestStore creates an in-memory MessageStore with FTS5 and seeds it with
// fixture data. The caller does not need to close it — cleanup is automatic.
func newTestStore(t *testing.T) *MessageStore {
	t.Helper()

	db, err := sql.Open("sqlite3", "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=5000")

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
		CREATE TABLE IF NOT EXISTS group_participants (
			group_jid TEXT,
			participant_jid TEXT,
			PRIMARY KEY (group_jid, participant_jid)
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		content,
		content='messages',
		content_rowid='rowid'
	)`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
	END`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', old.rowid, old.content);
	END`)
	db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', old.rowid, old.content);
		INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
	END`)

	seedFixtures(t, db)

	return &MessageStore{db: db}
}

// newTestStoreWithContacts creates a test store plus a contacts DB for
// participant-name search tests.
func newTestStoreWithContacts(t *testing.T) *MessageStore {
	t.Helper()
	store := newTestStore(t)

	contactsDB, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open contacts db: %v", err)
	}
	t.Cleanup(func() { contactsDB.Close() })

	contactsDB.Exec(`CREATE TABLE IF NOT EXISTS whatsmeow_contacts (
		their_jid TEXT PRIMARY KEY,
		full_name TEXT,
		push_name TEXT
	)`)

	contactsDB.Exec(`INSERT INTO whatsmeow_contacts VALUES ('11111@s.whatsapp.net', 'Alice Smith', 'Alice')`)
	contactsDB.Exec(`INSERT INTO whatsmeow_contacts VALUES ('22222@s.whatsapp.net', 'Bob Jones', 'Bobby')`)
	contactsDB.Exec(`INSERT INTO whatsmeow_contacts VALUES ('33333@s.whatsapp.net', 'Charlie Brown', 'Charlie')`)

	store.contactsDB = contactsDB
	return store
}

func seedFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now()

	chats := []struct {
		jid, name string
		lastTime  time.Time
	}{
		{"11111@s.whatsapp.net", "Alice Smith", now.Add(-1 * time.Hour)},
		{"22222@s.whatsapp.net", "Bob Jones", now.Add(-2 * time.Hour)},
		{"group1@g.us", "Family Chat", now.Add(-30 * time.Minute)},
		{"group2@g.us", "Work Team", now.Add(-3 * time.Hour)},
	}
	for _, c := range chats {
		db.Exec("INSERT INTO chats VALUES (?, ?, ?)", c.jid, c.name, c.lastTime.Format(time.RFC3339))
	}

	messages := []struct {
		id, chatJID, sender, content string
		ts                          time.Time
		isFromMe                    bool
		mediaType                   string
	}{
		{"m1", "11111@s.whatsapp.net", "11111", "Hello Alice", now.Add(-1 * time.Hour), false, ""},
		{"m2", "11111@s.whatsapp.net", "me", "Hi there", now.Add(-59 * time.Minute), true, ""},
		{"m3", "11111@s.whatsapp.net", "11111", "How are you doing today?", now.Add(-58 * time.Minute), false, ""},
		{"m4", "group1@g.us", "11111", "Family dinner tonight", now.Add(-30 * time.Minute), false, ""},
		{"m5", "group1@g.us", "22222", "Sounds great!", now.Add(-29 * time.Minute), false, ""},
		{"m6", "22222@s.whatsapp.net", "22222", "Check this photo", now.Add(-2 * time.Hour), false, "image"},
		{"m7", "group2@g.us", "33333", "Meeting at 3pm", now.Add(-3 * time.Hour), false, ""},
		{"m8", "group2@g.us", "me", "I'll be there", now.Add(-2*time.Hour - 50*time.Minute), true, ""},
	}
	for _, m := range messages {
		db.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.id, m.chatJID, m.sender, m.content, m.ts.Format(time.RFC3339), m.isFromMe,
			sql.NullString{String: m.mediaType, Valid: m.mediaType != ""})
	}

	participants := []struct{ group, jid string }{
		{"group1@g.us", "11111@s.whatsapp.net"},
		{"group1@g.us", "22222@s.whatsapp.net"},
		{"group2@g.us", "33333@s.whatsapp.net"},
	}
	for _, p := range participants {
		db.Exec("INSERT INTO group_participants VALUES (?, ?)", p.group, p.jid)
	}
}

// newTestService creates an MCPService backed by an in-memory test store
// and the given API URL (typically a httptest server URL).
func newTestService(t *testing.T, apiURL string) *MCPService {
	t.Helper()
	store := newTestStore(t)
	return NewMCPService(store, apiURL)
}

func newTestServiceWithContacts(t *testing.T, apiURL string) *MCPService {
	t.Helper()
	store := newTestStoreWithContacts(t)
	return NewMCPService(store, apiURL)
}

// requireContains fails the test if s does not contain substr.
func requireContains(t *testing.T, s, substr string) {
	t.Helper()
	if !containsSubstring(s, substr) {
		t.Errorf("expected %q to contain %q", truncate(s, 200), substr)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("... (%d bytes)", len(s))
}
