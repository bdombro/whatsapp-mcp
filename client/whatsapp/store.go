package whatsapp

import (
	"database/sql"
	"strings"
	"time"

	"whatsapp-client/core"
)

// Message is a raw row from the messages table.
type Message struct {
	Time      time.Time
	Sender    string
	Content   string
	IsFromMe  bool
	MediaType string
	Filename  string
}

// MessageStore wraps the messages SQLite database and optionally the
// whatsmeow contacts database for name resolution.
type MessageStore struct {
	db         *sql.DB
	contactsDB *sql.DB
}

// Close releases the message and contacts database handles so callers do not leak SQLite connections.
func (store *MessageStore) Close() error {
	if store.contactsDB != nil {
		store.contactsDB.Close()
	}
	return store.db.Close()
}

// StoreChat upserts one chat row with its latest known display name and last-message time.
func (store *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := store.db.Exec(
		"INSERT OR REPLACE INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)",
		jid, name, lastMessageTime,
	)
	return err
}

// StoreMessage upserts one message row, including optional media metadata, unless the payload is empty.
func (store *MessageStore) StoreMessage(id, chatJID, sender, content string, timestamp time.Time, isFromMe bool,
	mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	if content == "" && mediaType == "" {
		return nil
	}
	if content != "" && core.Looks2FA(content) {
		content = core.TwoFARedactedPlaceholder
	}

	_, err := store.db.Exec(
		`INSERT OR REPLACE INTO messages 
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, chatJID, sender, content, timestamp, isFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength,
	)
	return err
}

// GetMessages returns recent messages for one chat so callers can render chat history.
func (store *MessageStore) GetMessages(chatJID string, limit int) ([]Message, error) {
	rows, err := store.db.Query(
		"SELECT sender, content, timestamp, is_from_me, media_type, filename FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT ?",
		chatJID, limit,
	)
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var timestamp time.Time
		err := rows.Scan(&msg.Sender, &msg.Content, &timestamp, &msg.IsFromMe, &msg.MediaType, &msg.Filename)
		if err != nil { // nocov
			return nil, err
		}
		msg.Time = timestamp
		messages = append(messages, msg)
	}

	return messages, nil
}

// GetChats returns chat last-message times keyed by JID for callers that need simple chat summaries.
func (store *MessageStore) GetChats() (map[string]time.Time, error) {
	rows, err := store.db.Query("SELECT jid, last_message_time FROM chats ORDER BY last_message_time DESC")
	if err != nil { // nocov
		return nil, err
	}
	defer rows.Close()

	chats := make(map[string]time.Time)
	for rows.Next() {
		var jid string
		var lastMessageTime time.Time
		err := rows.Scan(&jid, &lastMessageTime)
		if err != nil { // nocov
			return nil, err
		}
		chats[jid] = lastMessageTime
	}

	return chats, nil
}

// StoreMediaInfo updates stored media download metadata for one message row.
func (store *MessageStore) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := store.db.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID,
	)
	return err
}

// GetMediaInfo returns stored media metadata for one message so downloads can be reconstructed later.
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

// looksLikeGroupPlaceholder flags fallback group names so chat-name resolution knows they are safe to replace.
func looksLikeGroupPlaceholder(name string) bool {
	return strings.HasPrefix(name, "Group ") && looksLikePhoneNumber(strings.TrimPrefix(name, "Group "))
}

// isSynthesizedName flags synthesized display names so real chat/contact names can replace them during later resolution.
func isSynthesizedName(name string) bool {
	return strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")")
}

// StoreGroupParticipants replaces stored participant membership for one group from a fresh sync snapshot.
func (store *MessageStore) StoreGroupParticipants(groupJID string, participantJIDs []string) error {
	tx, err := store.db.Begin()
	if err != nil { // nocov
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM group_participants WHERE group_jid = ?", groupJID)

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO group_participants (group_jid, participant_jid) VALUES (?, ?)")
	if err != nil { // nocov
		return err
	}
	defer stmt.Close()

	for _, pJID := range participantJIDs {
		stmt.Exec(groupJID, pJID)
	}
	return tx.Commit()
}

// looksLikePhoneNumber returns true if s consists entirely of digits (and optional leading '+').
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
func (store *MessageStore) GetSenderName(senderJID string) string {
	phonePart := senderJID
	if idx := strings.Index(senderJID, "@"); idx != -1 {
		phonePart = senderJID[:idx]
	}

	var name sql.NullString
	_ = store.db.QueryRow("SELECT name FROM chats WHERE jid = ? LIMIT 1", senderJID).Scan(&name)
	if name.Valid && name.String != "" && !looksLikePhoneNumber(name.String) {
		return name.String
	}

	_ = store.db.QueryRow("SELECT name FROM chats WHERE jid LIKE ? LIMIT 1", "%"+phonePart+"%").Scan(&name)
	if name.Valid && name.String != "" && !looksLikePhoneNumber(name.String) {
		return name.String
	}

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
			return pn
		}
	}

	return senderJID
}
