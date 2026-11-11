package whatsapp

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // register SQLite driver for database/sql
)

// privateDataFiles are tightened to 0600 by secureDataDir when they exist (older installs created them 0644).
var privateDataFiles = []string{
	"messages.db", "messages.db-wal", "messages.db-shm",
	"whatsapp.db", "whatsapp.db-wal", "whatsapp.db-shm",
	"daemon.log", "mcp-audit.log", "config.json",
}

// secureDataDir creates dataDir owner-only (0700) and tightens an existing dir and its known files, because they hold the
// session keys and every message. SQLite and launchd create files with the default umask, so the dir mode is what protects them.
func secureDataDir(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return err
	}
	for _, name := range privateDataFiles {
		os.Chmod(filepath.Join(dataDir, name), 0o600) // missing files are fine
	}
	return nil
}

// NewMessageStore opens (or creates) the message and contacts databases.
func NewMessageStore(dataDir string) (*MessageStore, error) {
	if err := secureDataDir(dataDir); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(on)", filepath.Join(dataDir, "messages.db")))
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")

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
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
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

	var msgCount int
	db.QueryRow("SELECT COUNT(*) FROM messages WHERE content != ''").Scan(&msgCount)
	if msgCount > 0 {
		var indexed int
		db.QueryRow("SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH '*'").Scan(&indexed)
		if indexed == 0 {
			db.Exec("INSERT INTO messages_fts(messages_fts) VALUES('rebuild')")
		}
	}

	store := &MessageStore{db: db}

	// Open whatsmeow contacts DB (read-only) for name resolution; non-fatal if missing.
	contactsDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(30000)", filepath.Join(dataDir, "whatsapp.db")))
	if err == nil {
		store.contactsDB = contactsDB
	}

	return store, nil
}

// NewMessageStoreFromDB creates a MessageStore directly from an existing
// database connection. Used by tests to inject in-memory databases.
func NewMessageStoreFromDB(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}
