package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Message struct {
	Time      time.Time
	Sender    string
	Content   string
	IsFromMe  bool
	MediaType string
	Filename  string
}

type MessageStore struct {
	db         *sql.DB
	contactsDB *sql.DB
}

func NewMessageStore() (*MessageStore, error) {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}

	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", filepath.Join(dir, "messages.db")))
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	// Enable WAL mode and busy timeout to prevent locking issues when core
	// daemon and sync (or two syncs) access the database concurrently.
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
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	// FTS5 full-text index for BM25 keyword search.
	// External content table backed by messages — kept in sync via triggers.
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

	// Rebuild FTS index if empty. External content tables return rows from the
	// source table on SELECT, so we probe the actual index with a token query.
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

	// Open whatsmeow contacts DB (read-only) for name resolution; non-fatal if missing
	contactsDB, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", filepath.Join(dir, "whatsapp.db")))
	if err == nil {
		store.contactsDB = contactsDB
	}

	return store, nil
}

func (store *MessageStore) Close() error {
	if store.contactsDB != nil {
		store.contactsDB.Close()
	}
	return store.db.Close()
}

func (store *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := store.db.Exec(
		"INSERT OR REPLACE INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)",
		jid, name, lastMessageTime,
	)
	return err
}

func (store *MessageStore) StoreMessage(id, chatJID, sender, content string, timestamp time.Time, isFromMe bool,
	mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	if content == "" && mediaType == "" {
		return nil
	}

	_, err := store.db.Exec(
		`INSERT OR REPLACE INTO messages 
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, chatJID, sender, content, timestamp, isFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength,
	)
	return err
}

func (store *MessageStore) GetMessages(chatJID string, limit int) ([]Message, error) {
	rows, err := store.db.Query(
		"SELECT sender, content, timestamp, is_from_me, media_type, filename FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT ?",
		chatJID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var timestamp time.Time
		err := rows.Scan(&msg.Sender, &msg.Content, &timestamp, &msg.IsFromMe, &msg.MediaType, &msg.Filename)
		if err != nil {
			return nil, err
		}
		msg.Time = timestamp
		messages = append(messages, msg)
	}

	return messages, nil
}

func (store *MessageStore) GetChats() (map[string]time.Time, error) {
	rows, err := store.db.Query("SELECT jid, last_message_time FROM chats ORDER BY last_message_time DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := make(map[string]time.Time)
	for rows.Next() {
		var jid string
		var lastMessageTime time.Time
		err := rows.Scan(&jid, &lastMessageTime)
		if err != nil {
			return nil, err
		}
		chats[jid] = lastMessageTime
	}

	return chats, nil
}

func (store *MessageStore) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := store.db.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID,
	)
	return err
}

func (store *MessageStore) GetMediaInfo(id, chatJID string) (string, string, string, []byte, []byte, []byte, uint64, error) {
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64

	err := store.db.QueryRow(
		"SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA256, &fileEncSHA256, &fileLength)

	return mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err
}

// looksLikeGroupPlaceholder returns true for names like "Group 120363364944939917"
// that were stored as fallbacks when GetGroupInfo failed.
func looksLikeGroupPlaceholder(name string) bool {
	return strings.HasPrefix(name, "Group ") && looksLikePhoneNumber(strings.TrimPrefix(name, "Group "))
}

// isSynthesizedName returns true for parenthesized names like "(Kevin, Eileen)"
// that were generated from participant lists when the WhatsApp API didn't
// return a group name.
func isSynthesizedName(name string) bool {
	return strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")")
}

func (store *MessageStore) StoreGroupParticipants(groupJID string, participantJIDs []string) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing participants for this group before inserting fresh data
	tx.Exec("DELETE FROM group_participants WHERE group_jid = ?", groupJID)

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO group_participants (group_jid, participant_jid) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, pJID := range participantJIDs {
		stmt.Exec(groupJID, pJID)
	}
	return tx.Commit()
}

// looksLikePhoneNumber returns true if s consists entirely of digits (and optional leading '+').
// These are placeholder names from when the contact store wasn't synced yet.
func looksLikePhoneNumber(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '+' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	for _, c := range s[start:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// GetSenderName resolves a sender JID (or bare phone number) to a display name.
// Resolution order:
//  1. Exact match in messages.db chats table (skip if name is just a phone number)
//  2. LIKE match in messages.db chats table (skip if name is just a phone number)
//  3. whatsmeow_contacts in whatsapp.db by full JID or phone number suffix
//  4. Falls back to the original sender string
func (store *MessageStore) GetSenderName(senderJID string) string {
	phonePart := senderJID
	if idx := strings.Index(senderJID, "@"); idx != -1 {
		phonePart = senderJID[:idx]
	}

	// 1. Try messages.db chats table
	var name sql.NullString
	_ = store.db.QueryRow("SELECT name FROM chats WHERE jid = ? LIMIT 1", senderJID).Scan(&name)
	if name.Valid && name.String != "" && !looksLikePhoneNumber(name.String) {
		return name.String
	}

	_ = store.db.QueryRow("SELECT name FROM chats WHERE jid LIKE ? LIMIT 1", "%"+phonePart+"%").Scan(&name)
	if name.Valid && name.String != "" && !looksLikePhoneNumber(name.String) {
		return name.String
	}

	// 2. Try whatsmeow_contacts in whatsapp.db
	if store.contactsDB != nil {
		contactsDB := store.contactsDB
		candidates := []string{
			senderJID,
			phonePart + "@s.whatsapp.net",
			phonePart + "@lid",
		}
		for _, jid := range candidates {
			var fullName, pushName sql.NullString
			err := contactsDB.QueryRow(
				"SELECT full_name, push_name FROM whatsmeow_contacts WHERE their_jid = ? LIMIT 1", jid,
			).Scan(&fullName, &pushName)
			if err != nil {
				continue
			}
			if fullName.Valid && fullName.String != "" {
				return fullName.String
			}
			if pushName.Valid && pushName.String != "" {
				return pushName.String
			}
		}

		// For LID-based senders, try the LID→phone mapping then re-lookup
		var mappedPhone sql.NullString
		_ = contactsDB.QueryRow(
			"SELECT pn FROM whatsmeow_lid_map WHERE lid = ? LIMIT 1", phonePart,
		).Scan(&mappedPhone)
		if mappedPhone.Valid && mappedPhone.String != "" {
			pn := mappedPhone.String
			var fullName, pushName sql.NullString
			err := contactsDB.QueryRow(
				"SELECT full_name, push_name FROM whatsmeow_contacts WHERE their_jid = ? LIMIT 1",
				pn+"@s.whatsapp.net",
			).Scan(&fullName, &pushName)
			if err == nil {
				if fullName.Valid && fullName.String != "" {
					return fullName.String
				}
				if pushName.Valid && pushName.String != "" {
					return pushName.String
				}
			}
			// No contact entry but we resolved the phone — return that
			// instead of the opaque LID.
			return pn
		}
	}

	return senderJID
}
