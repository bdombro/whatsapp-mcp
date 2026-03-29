package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// isGroupJID returns true if the JID refers to a WhatsApp group chat.
func isGroupJID(jid string) bool {
	return strings.HasSuffix(jid, "@g.us")
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

// upgradeGroupName updates a group's name in the chats table only if the
// current name is a placeholder (e.g. "Group 120363...") or a synthesized
// name (e.g. "(Kevin, Eileen)"). Returns true if the name was actually updated.
func (store *MessageStore) upgradeGroupName(groupJID, newName string) bool {
	var current sql.NullString
	store.db.QueryRow("SELECT name FROM chats WHERE jid = ?", groupJID).Scan(&current)
	if !current.Valid {
		return false
	}
	if !looksLikeGroupPlaceholder(current.String) && !isSynthesizedName(current.String) {
		return false
	}
	store.db.Exec("UPDATE chats SET name = ? WHERE jid = ?", newName, groupJID)
	return true
}

// synthesizeGroupName builds a descriptive name from a group's participant
// list for groups where the WhatsApp API doesn't return a name.
// Returns empty string if no usable participants are found.
func (store *MessageStore) synthesizeGroupName(groupJID string) string {
	participants := store.GetGroupParticipants(groupJID)
	if len(participants) == 0 {
		// Fall back to unique message senders
		rows, err := store.db.Query(
			"SELECT DISTINCT sender FROM messages WHERE chat_jid = ? AND is_from_me = 0",
			groupJID,
		)
		if err != nil {
			return ""
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				participants = append(participants, s)
			}
		}
	}

	var resolvedNames []string
	var phoneOnly []string
	for _, p := range participants {
		info := store.ResolveParticipant(p)
		if info.Name != "" && info.Name != ownerName {
			label := info.Name
			if idx := strings.Index(label, " "); idx > 0 {
				label = label[:idx]
			}
			resolvedNames = append(resolvedNames, label)
		} else if info.Phone != "" {
			phoneOnly = append(phoneOnly, info.Phone)
		}
	}

	names := resolvedNames
	if len(names) == 0 {
		names = phoneOnly
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	if len(names) > 4 {
		names = names[:4]
	}
	return "(" + strings.Join(names, ", ") + ")"
}

// isGroupSender returns true if a bare sender (no @-suffix) corresponds to a
// known group chat in the database, meaning the history sync recorded the group
// itself as the message sender rather than the actual individual.
func (store *MessageStore) isGroupSender(sender string) bool {
	candidates := []string{
		sender + "@g.us",
		sender,
	}
	for _, jid := range candidates {
		var count int
		store.db.QueryRow("SELECT COUNT(*) FROM chats WHERE jid = ?", jid).Scan(&count)
		if count > 0 && isGroupJID(jid) {
			return true
		}
	}
	return false
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

func (store *MessageStore) GetGroupParticipants(groupJID string) []string {
	rows, err := store.db.Query("SELECT participant_jid FROM group_participants WHERE group_jid = ?", groupJID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var participants []string
	for rows.Next() {
		var jid string
		if rows.Scan(&jid) == nil {
			participants = append(participants, jid)
		}
	}
	return participants
}

// looksLikeUnresolved returns true if s looks like a phone number or a raw JID
// rather than a human-readable name.
func looksLikeUnresolved(s string) bool {
	return looksLikePhoneNumber(s) ||
		strings.HasSuffix(s, "@lid") ||
		strings.HasSuffix(s, "@s.whatsapp.net") ||
		strings.HasSuffix(s, "@g.us")
}

// ParticipantInfo holds the resolved identifiers for a single participant.
type ParticipantInfo struct {
	Name  string // human-readable name, empty if unresolved
	Phone string // phone number, empty if unknown
	LID   string // LID (without @lid suffix), empty if unknown
}

// ResolveParticipant resolves a participant JID into all available identifiers.
func (store *MessageStore) ResolveParticipant(jid string) ParticipantInfo {
	var info ParticipantInfo

	barePart := jid
	if idx := strings.Index(jid, "@"); idx != -1 {
		barePart = jid[:idx]
	}

	isLID := strings.HasSuffix(jid, "@lid")

	if isLID {
		info.LID = barePart
		// LID → phone via lid_map
		if store.contactsDB != nil {
			var pn sql.NullString
			store.contactsDB.QueryRow(
				"SELECT pn FROM whatsmeow_lid_map WHERE lid = ? LIMIT 1", barePart,
			).Scan(&pn)
			if pn.Valid && pn.String != "" {
				info.Phone = pn.String
			}
		}
	} else {
		// Assume it's a phone number (or phone@s.whatsapp.net)
		info.Phone = barePart
		// Reverse lookup: phone → LID
		if store.contactsDB != nil {
			var lid sql.NullString
			store.contactsDB.QueryRow(
				"SELECT lid FROM whatsmeow_lid_map WHERE pn = ? LIMIT 1", barePart,
			).Scan(&lid)
			if lid.Valid && lid.String != "" {
				info.LID = lid.String
			}
		}
	}

	// Resolve name via all available identifiers
	name := store.GetSenderName(jid)
	if !looksLikeUnresolved(name) {
		info.Name = name
	} else if info.Phone != "" && info.Phone != barePart {
		// Try resolving via the phone we discovered
		name = store.GetSenderName(info.Phone + "@s.whatsapp.net")
		if !looksLikeUnresolved(name) {
			info.Name = name
		}
	}

	return info
}

// FormatParticipantLine formats a participant line as "  - {name}; {phone}; lid:{lid}"
// omitting fields that are empty. The LID is prefixed with "lid:" to distinguish
// it from phone numbers (both are all-digit strings).
func FormatParticipantLine(info ParticipantInfo) string {
	var parts []string
	if info.Name != "" {
		parts = append(parts, info.Name)
	}
	if info.Phone != "" {
		parts = append(parts, info.Phone)
	}
	if info.LID != "" {
		parts = append(parts, "lid:"+info.LID)
	}
	if len(parts) == 0 {
		return "  - (unknown)"
	}
	return "  - " + strings.Join(parts, "; ")
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
