package whatsapp

import (
	"database/sql"
	"testing"
	"time"

	"whatsapp-client/core"

	_ "modernc.org/sqlite"
)

// Builds an in-memory message store with the minimal schema and triggers needed for store-level tests.
func newTestMessageStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
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

	// Create contacts.db for testing
	contactsDB, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("open contacts db: %v", err)
	}
	t.Cleanup(func() { contactsDB.Close() })

	_, err = contactsDB.Exec(`
		CREATE TABLE IF NOT EXISTS contacts (
			jid TEXT PRIMARY KEY,
			name TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create contacts table: %v", err)
	}

	return &MessageStore{db: db, contactsDB: contactsDB}
}

// Verifies storing a chat persists its name and last-message timestamp.
func TestStoreChat(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()

	err := store.StoreChat("group1@g.us", "Family Group", ts)
	if err != nil {
		t.Fatalf("StoreChat: %v", err)
	}

	var name string
	var lastMsg time.Time
	err = store.db.QueryRow("SELECT name, last_message_time FROM chats WHERE jid = ?", "group1@g.us").Scan(&name, &lastMsg)
	if err != nil {
		t.Fatalf("query chat: %v", err)
	}

	if name != "Family Group" {
		t.Errorf("expected 'Family Group', got %q", name)
	}
	if !lastMsg.Equal(ts) {
		t.Errorf("expected timestamp %v, got %v", ts, lastMsg)
	}
}

// Verifies storing the same chat again updates the existing row instead of creating duplicates.
func TestStoreChat_upsert(t *testing.T) {
	store := newTestMessageStore(t)
	ts1 := time.Now().Add(-1 * time.Hour)
	ts2 := time.Now()

	store.StoreChat("group1@g.us", "Old Name", ts1)
	store.StoreChat("group1@g.us", "New Name", ts2)

	var name string
	store.db.QueryRow("SELECT name FROM chats WHERE jid = ?", "group1@g.us").Scan(&name)
	if name != "New Name" {
		t.Errorf("expected 'New Name', got %q", name)
	}
}

// Verifies storing a message persists the content row after the parent chat exists.
func TestStoreMessage(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()

	// Store chat first
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts)

	err := store.StoreMessage("msg1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "Hello world", ts, false, "", "", "", nil, nil, nil, 0)
	if err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}

	var content string
	err = store.db.QueryRow("SELECT content FROM messages WHERE id = ?", "msg1").Scan(&content)
	if err != nil {
		t.Fatalf("query message: %v", err)
	}

	if content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", content)
	}
}

// Verifies OTP-style message bodies are replaced before persistence so codes are not stored or indexed.
func TestStoreMessage_redacts2FA(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts)
	otpText := "Your verification code is 123456"
	err := store.StoreMessage("otp1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", otpText, ts, false, "", "", "", nil, nil, nil, 0)
	if err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}
	var content string
	if err := store.db.QueryRow("SELECT content FROM messages WHERE id = ?", "otp1").Scan(&content); err != nil {
		t.Fatalf("query: %v", err)
	}
	if content != core.TwoFARedactedPlaceholder {
		t.Fatalf("expected redacted placeholder, got %q", content)
	}
}

// Verifies storing the same message again updates the existing row instead of duplicating it.
func TestStoreMessage_upsert(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts)

	store.StoreMessage("msg1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "First", ts, false, "", "", "", nil, nil, nil, 0)
	store.StoreMessage("msg1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "Updated", ts, false, "", "", "", nil, nil, nil, 0)

	var content string
	store.db.QueryRow("SELECT content FROM messages WHERE id = ?", "msg1").Scan(&content)
	if content != "Updated" {
		t.Errorf("expected 'Updated', got %q", content)
	}
}

// Verifies message listing returns chat messages in reverse chronological order for recent-history views.
func TestGetMessages(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts)

	store.StoreMessage("msg1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "First", ts.Add(-2*time.Hour), false, "", "", "", nil, nil, nil, 0)
	store.StoreMessage("msg2", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "Second", ts.Add(-1*time.Hour), false, "", "", "", nil, nil, nil, 0)

	msgs, err := store.GetMessages("11111@s.whatsapp.net", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// Messages should be in reverse chronological order (newest first)
	if msgs[0].Content != "Second" {
		t.Errorf("expected 'Second' first, got %q", msgs[0].Content)
	}
	if msgs[1].Content != "First" {
		t.Errorf("expected 'First' second, got %q", msgs[1].Content)
	}
}

// Verifies chat listing returns all stored chats keyed by JID.
func TestGetChats(t *testing.T) {
	store := newTestMessageStore(t)
	ts1 := time.Now().Add(-2 * time.Hour)
	ts2 := time.Now().Add(-1 * time.Hour)

	store.StoreChat("group1@g.us", "Family", ts1)
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts2)

	chats, err := store.GetChats()
	if err != nil {
		t.Fatalf("GetChats: %v", err)
	}

	if len(chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chats))
	}

	if _, ok := chats["group1@g.us"]; !ok {
		t.Error("expected group1@g.us in chats")
	}
	if _, ok := chats["11111@s.whatsapp.net"]; !ok {
		t.Error("expected 11111@s.whatsapp.net in chats")
	}
}

// Verifies media metadata updates persist URL and file length on an existing message row.
func TestStoreMediaInfo(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts)

	store.StoreMessage("msg1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "", ts, false, "image", "", "", nil, nil, nil, 0)

	err := store.StoreMediaInfo("msg1", "11111@s.whatsapp.net", "http://example.com/image.jpg", []byte("key"), []byte("sha256"), []byte("encsha256"), 12345)
	if err != nil {
		t.Fatalf("StoreMediaInfo: %v", err)
	}

	var url string
	var fileLength uint64
	err = store.db.QueryRow("SELECT url, file_length FROM messages WHERE id = ?", "msg1").Scan(&url, &fileLength)
	if err != nil {
		t.Fatalf("query media info: %v", err)
	}

	if url != "http://example.com/image.jpg" {
		t.Errorf("expected 'http://example.com/image.jpg', got %q", url)
	}
	if fileLength != 12345 {
		t.Errorf("expected 12345, got %d", fileLength)
	}
}

// Verifies media-info lookups return the stored metadata needed for later download requests.
func TestGetMediaInfo(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts)

	store.StoreMessage("msg1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "", ts, false, "image", "", "", nil, nil, nil, 0)
	store.StoreMediaInfo("msg1", "11111@s.whatsapp.net", "http://example.com/image.jpg", []byte("key"), []byte("sha256"), []byte("encsha256"), 12345)

	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err := store.GetMediaInfo("msg1", "11111@s.whatsapp.net")
	if err != nil {
		t.Fatalf("GetMediaInfo: %v", err)
	}

	if mediaType != "image" {
		t.Errorf("expected 'image', got %q", mediaType)
	}
	if url != "http://example.com/image.jpg" {
		t.Errorf("expected 'http://example.com/image.jpg', got %q", url)
	}
	if string(mediaKey) != "key" {
		t.Errorf("expected 'key', got %q", string(mediaKey))
	}
	if fileLength != 12345 {
		t.Errorf("expected 12345, got %d", fileLength)
	}
	_ = filename
	_ = fileSHA256
	_ = fileEncSHA256
}

// Verifies storing group participants persists every participant row for the target group.
func TestStoreGroupParticipants(t *testing.T) {
	store := newTestMessageStore(t)

	participants := []string{"11111@s.whatsapp.net", "22222@s.whatsapp.net"}
	err := store.StoreGroupParticipants("group1@g.us", participants)
	if err != nil {
		t.Fatalf("StoreGroupParticipants: %v", err)
	}

	var count int
	err = store.db.QueryRow("SELECT COUNT(*) FROM group_participants WHERE group_jid = ?", "group1@g.us").Scan(&count)
	if err != nil {
		t.Fatalf("query participants: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 participants, got %d", count)
	}
}

// Verifies storing group participants again replaces the previous participant set for that group.
func TestStoreGroupParticipants_upsert(t *testing.T) {
	store := newTestMessageStore(t)

	// First set
	store.StoreGroupParticipants("group1@g.us", []string{"11111@s.whatsapp.net"})

	// Second set should replace
	store.StoreGroupParticipants("group1@g.us", []string{"22222@s.whatsapp.net", "33333@s.whatsapp.net"})

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM group_participants WHERE group_jid = ?", "group1@g.us").Scan(&count)

	if count != 2 {
		t.Errorf("expected 2 participants after upsert, got %d", count)
	}

	var jid string
	store.db.QueryRow("SELECT participant_jid FROM group_participants WHERE group_jid = ? LIMIT 1", "group1@g.us").Scan(&jid)
	if jid == "11111@s.whatsapp.net" {
		t.Error("old participant should be removed")
	}
}

// Verifies placeholder detection distinguishes synthesized group labels from real chat names.
func TestLooksLikeGroupPlaceholder(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"group placeholder", "Group 1234567890", true},
		{"group with plus", "Group +1234567890", true},
		{"real name", "Alice Smith", false},
		{"contains group but not placeholder", "Group Chat", false},
		{"just 'Group '", "Group ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := looksLikeGroupPlaceholder(tt.input)
			if result != tt.expected {
				t.Errorf("looksLikeGroupPlaceholder(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// Verifies synthesized-name detection catches parenthesized generated names without mislabeling normal contacts.
func TestIsSynthesizedName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"parenthesized", "(Alice, Bob)", true},
		{"single name parenthesized", "(Alice)", true},
		{"phone number", "+1234567890", false},
		{"number only", "1234567890", false},
		{"real name", "Alice Smith", false},
		{"empty", "", false},
		{"only opening", "(Alice", false},
		{"only closing", "Alice)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSynthesizedName(tt.input)
			if result != tt.expected {
				t.Errorf("isSynthesizedName(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// Verifies phone-number detection accepts numeric identifiers and rejects obvious non-phone names.
func TestLooksLikePhoneNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"phone with plus", "+1234567890", true},
		{"just digits", "1234567890", true},
		{"short number", "12345", true},               // No length check in implementation
		{"long number", "12345678901234567890", true}, // No length check in implementation
		{"contains letters", "123abc456", false},
		{"real name", "Alice", false},
		{"empty", "", false},
		{"just plus", "+", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := looksLikePhoneNumber(tt.input)
			if result != tt.expected {
				t.Errorf("looksLikePhoneNumber(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// Verifies sender-name resolution prefers richer contact names and falls back to normalized identifiers when needed.
func TestGetSenderName(t *testing.T) {
	store := newTestMessageStore(t)

	// Create whatsmeow_contacts table
	_, err := store.contactsDB.Exec(`
		CREATE TABLE IF NOT EXISTS whatsmeow_contacts (
			their_jid TEXT PRIMARY KEY,
			full_name TEXT,
			push_name TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create contacts table: %v", err)
	}

	// Add a contact
	_, err = store.contactsDB.Exec("INSERT INTO whatsmeow_contacts (their_jid, full_name, push_name) VALUES (?, ?, ?)", "11111@s.whatsapp.net", "Alice Smith", "Alice")
	if err != nil {
		t.Fatalf("insert contact: %v", err)
	}

	tests := []struct {
		name     string
		jid      string
		expected string
	}{
		{"known contact with full name", "11111@s.whatsapp.net", "Alice Smith"},
		{"known contact phone only", "11111", "Alice Smith"},
		{"unknown contact", "99999@s.whatsapp.net", "99999@s.whatsapp.net"},
		{"unknown phone only", "99999", "99999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := store.GetSenderName(tt.jid)
			if result != tt.expected {
				t.Errorf("GetSenderName(%q) = %q, expected %q", tt.jid, result, tt.expected)
			}
		})
	}
}

// Verifies closing the store closes both backing databases so later queries fail fast.
func TestMessageStoreClose(t *testing.T) {
	store := newTestMessageStore(t)
	err := store.Close()
	if err != nil {
		t.Errorf("Close() error: %v", err)
	}

	// Verify databases are closed by trying to use them
	_, err = store.db.Query("SELECT 1")
	if err == nil {
		t.Error("expected error querying closed db")
	}
}

// Verifies empty non-media messages are skipped instead of polluting the message table.
func TestStoreMessage_skipEmpty(t *testing.T) {
	store := newTestMessageStore(t)
	ts := time.Now()
	store.StoreChat("11111@s.whatsapp.net", "Alice", ts)

	err := store.StoreMessage("skip1", "11111@s.whatsapp.net", "11111@s.whatsapp.net", "", ts, false, "", "", "", nil, nil, nil, 0)
	if err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}

	var count int
	store.db.QueryRow("SELECT COUNT(*) FROM messages WHERE id = 'skip1'").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 rows when content and mediaType are both empty, got %d", count)
	}
}

// Verifies sender-name resolution falls back to chat names when no richer contact row exists.
func TestGetSenderName_fromChatsTable(t *testing.T) {
	store := newTestMessageStore(t)
	store.StoreChat("77777@s.whatsapp.net", "Charlie Brown", time.Now())

	result := store.GetSenderName("77777@s.whatsapp.net")
	if result != "Charlie Brown" {
		t.Errorf("expected 'Charlie Brown', got %q", result)
	}
}

// Verifies sender-name resolution falls back to push names when full contact names are unavailable.
func TestGetSenderName_pushNameFallback(t *testing.T) {
	store := newTestMessageStore(t)

	store.contactsDB.Exec(`CREATE TABLE IF NOT EXISTS whatsmeow_contacts (
		their_jid TEXT PRIMARY KEY, full_name TEXT, push_name TEXT
	)`)
	store.contactsDB.Exec("INSERT OR REPLACE INTO whatsmeow_contacts VALUES (?, ?, ?)",
		"88888@s.whatsapp.net", "", "PushOnly")

	result := store.GetSenderName("88888@s.whatsapp.net")
	if result != "PushOnly" {
		t.Errorf("expected 'PushOnly', got %q", result)
	}
}

// Verifies LID-based sender lookup resolves through the lid map before applying normal contact-name fallbacks.
func TestGetSenderName_lidMapping(t *testing.T) {
	store := newTestMessageStore(t)

	store.contactsDB.Exec(`CREATE TABLE IF NOT EXISTS whatsmeow_contacts (
		their_jid TEXT PRIMARY KEY, full_name TEXT, push_name TEXT
	)`)
	store.contactsDB.Exec(`CREATE TABLE IF NOT EXISTS whatsmeow_lid_map (
		lid TEXT PRIMARY KEY, pn TEXT
	)`)
	store.contactsDB.Exec("INSERT OR REPLACE INTO whatsmeow_lid_map VALUES ('lid_test_123', '66666')")

	t.Run("lid with full_name", func(t *testing.T) {
		store.contactsDB.Exec("INSERT OR REPLACE INTO whatsmeow_contacts VALUES ('66666@s.whatsapp.net', 'LID FullName', 'LID Push')")
		result := store.GetSenderName("lid_test_123@lid")
		if result != "LID FullName" {
			t.Errorf("expected 'LID FullName', got %q", result)
		}
	})

	t.Run("lid with push_name only", func(t *testing.T) {
		store.contactsDB.Exec("UPDATE whatsmeow_contacts SET full_name = '' WHERE their_jid = '66666@s.whatsapp.net'")
		result := store.GetSenderName("lid_test_123@lid")
		if result != "LID Push" {
			t.Errorf("expected 'LID Push', got %q", result)
		}
	})

	t.Run("lid with no contact entry", func(t *testing.T) {
		store.contactsDB.Exec("DELETE FROM whatsmeow_contacts WHERE their_jid = '66666@s.whatsapp.net'")
		result := store.GetSenderName("lid_test_123@lid")
		if result != "66666" {
			t.Errorf("expected resolved phone '66666', got %q", result)
		}
	})
}
