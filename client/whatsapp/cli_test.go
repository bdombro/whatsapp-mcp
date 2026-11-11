package whatsapp

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

// Verifies WhatsApp info output reports an empty state before any session or message DB exists.
func TestInfoLines_emptyDir(t *testing.T) {
	dir := t.TempDir()
	lines := InfoLines(dir)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "not paired") {
		t.Errorf("session line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "no database yet") {
		t.Errorf("messages line: %q", lines[1])
	}
}

// Verifies WhatsApp info output reports session DB sizes and message/chat counts when local data exists.
func TestInfoLines_withSessionAndMessages(t *testing.T) {
	dir := t.TempDir()
	waPath := filepath.Join(dir, "whatsapp.db")
	msgPath := filepath.Join(dir, "messages.db")

	waDB, err := sql.Open("sqlite", waPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = waDB.Exec(`CREATE TABLE whatsmeow_device (jid TEXT); INSERT INTO whatsmeow_device VALUES ('user@s.whatsapp.net');`)
	if err != nil {
		t.Fatal(err)
	}
	waDB.Close()

	msgDB, err := sql.Open("sqlite", msgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = msgDB.Exec(`CREATE TABLE chats (id INTEGER); CREATE TABLE messages (id INTEGER);
		INSERT INTO chats VALUES (1),(2); INSERT INTO messages VALUES (1),(2),(3);`)
	if err != nil {
		t.Fatal(err)
	}
	msgDB.Close()

	lines := InfoLines(dir)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "user@s.whatsapp.net") {
		t.Errorf("session line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "DB:") || !strings.Contains(lines[1], "MB") {
		t.Errorf("db size line: %q", lines[1])
	}
	if !strings.Contains(lines[2], "3 across 2 chats") {
		t.Errorf("counts line: %q", lines[2])
	}
	if got := SessionAuthDisplay(dir); got != "user@s.whatsapp.net" {
		t.Fatalf("SessionAuthDisplay = %q", got)
	}
}

// Verifies WhatsApp info output reports unreadable message DBs without crashing the status command.
func TestInfoLines_messagesDBUnreadable(t *testing.T) {
	dir := t.TempDir()
	waPath := filepath.Join(dir, "whatsapp.db")
	waDB, err := sql.Open("sqlite", waPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = waDB.Exec(`CREATE TABLE whatsmeow_device (jid TEXT); INSERT INTO whatsmeow_device VALUES ('x@s.whatsapp.net');`)
	if err != nil {
		t.Fatal(err)
	}
	waDB.Close()

	msgPath := filepath.Join(dir, "messages.db")
	if err := os.WriteFile(msgPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	lines := InfoLines(dir)
	if len(lines) != 3 || !strings.Contains(lines[2], "unable to read database") {
		t.Fatalf("lines: %q", lines)
	}
}

// Verifies SessionAuthDisplay returns no without a session database.
func TestSessionAuthDisplay_empty(t *testing.T) {
	if got := SessionAuthDisplay(t.TempDir()); got != "no" {
		t.Fatalf("SessionAuthDisplay = %q", got)
	}
}

// Verifies pairedWhatsAppJID returns an error when whatsapp.db is absent so callers can distinguish missing DB from empty JID.
func TestPairedWhatsAppJID_missingDB(t *testing.T) {
	if _, err := pairedWhatsAppJID(t.TempDir()); err == nil {
		t.Fatal("expected error when whatsapp.db is missing")
	}
}

// Verifies pairedWhatsAppJID returns an error when whatsapp.db exists but is not a readable SQLite session store.
func TestPairedWhatsAppJID_invalidDB(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "whatsapp.db"), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pairedWhatsAppJID(dir); err == nil {
		t.Fatal("expected error for invalid sqlite file")
	}
	if got := SessionAuthDisplay(dir); got != "no" {
		t.Fatalf("SessionAuthDisplay = %q", got)
	}
}

// Verifies logout handling notifies the caller so the daemon can stop and wait for a new login.
func TestHandleLoggedOut_notifies(t *testing.T) {
	notified := false
	handleLoggedOut(waLog.Stdout("Test", "ERROR", false), func() {
		notified = true
	})
	if !notified {
		t.Fatal("expected logged-out handler to notify caller")
	}
}

// Verifies RunReset removes the WhatsApp session and message DBs.
func TestRunReset_removesDBs(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"messages.db",
		"messages.db-wal",
		"messages.db-shm",
		"whatsapp.db",
		"whatsapp.db-wal",
		"whatsapp.db-shm",
	}
	for _, rel := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("seed"), 0644); err != nil {
			t.Fatalf("WriteFile(%s): %v", rel, err)
		}
	}

	RunReset(dir)

	for _, rel := range files {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err = %v", rel, err)
		}
	}
}
