package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------- ListChats ----------

func TestListChats_noQuery(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) != 4 {
		t.Fatalf("expected 4 chats, got %d", len(chats))
	}
	if chats[0].JID != "group1@g.us" {
		t.Errorf("first chat should be group1 (most recent), got %s", chats[0].JID)
	}
}

func TestListChats_sortByName(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("", 10, 0, true, "name")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) < 2 {
		t.Fatal("expected at least 2 chats")
	}
	if chats[0].Name > chats[1].Name {
		t.Errorf("expected sorted by name, got %q before %q", chats[0].Name, chats[1].Name)
	}
}

func TestListChats_fuzzyName(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("Family", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) == 0 {
		t.Fatal("expected at least one chat matching 'Family'")
	}
	found := false
	for _, c := range chats {
		if c.JID == "group1@g.us" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find group1@g.us (Family Chat)")
	}
}

func TestListChats_fuzzyTypo(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("Famly", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	found := false
	for _, c := range chats {
		if c.JID == "group1@g.us" {
			found = true
		}
	}
	if !found {
		t.Error("expected fuzzy match 'Famly' → 'Family Chat'")
	}
}

func TestListChats_participantName(t *testing.T) {
	svc := newTestServiceWithContacts(t, "")
	chats, err := svc.ListChats("Alice", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	jids := make(map[string]bool)
	for _, c := range chats {
		jids[c.JID] = true
	}
	if !jids["group1@g.us"] {
		t.Error("expected group1@g.us (Alice is a participant)")
	}
	if !jids["11111@s.whatsapp.net"] {
		t.Error("expected 11111@s.whatsapp.net (Alice's direct chat)")
	}
}

func TestListChats_noMatch(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.ListChats("zzzznonexistent", 10, 0, true, "last_active")
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) != 0 {
		t.Errorf("expected 0 chats, got %d", len(chats))
	}
}

func TestListChats_pagination(t *testing.T) {
	svc := newTestService(t, "")
	page0, _ := svc.ListChats("", 2, 0, true, "last_active")
	page1, _ := svc.ListChats("", 2, 1, true, "last_active")
	if len(page0) != 2 {
		t.Errorf("page 0: expected 2, got %d", len(page0))
	}
	if len(page1) != 2 {
		t.Errorf("page 1: expected 2, got %d", len(page1))
	}
	if page0[0].JID == page1[0].JID {
		t.Error("pagination returned same first result")
	}
}

func TestListChats_isGroup(t *testing.T) {
	svc := newTestService(t, "")
	chats, _ := svc.ListChats("", 10, 0, false, "last_active")
	for _, c := range chats {
		expectedGroup := c.JID == "group1@g.us" || c.JID == "group2@g.us"
		if c.IsGroup != expectedGroup {
			t.Errorf("chat %s: IsGroup=%v, want %v", c.JID, c.IsGroup, expectedGroup)
		}
	}
}

// ---------- GetChat ----------

func TestGetChat_found(t *testing.T) {
	svc := newTestService(t, "")
	chat, err := svc.GetChat("11111@s.whatsapp.net", true)
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}
	if chat.Name != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %q", chat.Name)
	}
}

func TestGetChat_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetChat("nonexistent@s.whatsapp.net", true)
	if err == nil {
		t.Error("expected error for non-existent chat")
	}
}

// ---------- GetDirectChatByContact ----------

func TestGetDirectChatByContact_found(t *testing.T) {
	svc := newTestService(t, "")
	chat, err := svc.GetDirectChatByContact("11111")
	if err != nil {
		t.Fatalf("GetDirectChatByContact: %v", err)
	}
	if chat.Name != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %q", chat.Name)
	}
	if chat.IsGroup {
		t.Error("expected non-group chat")
	}
}

func TestGetDirectChatByContact_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetDirectChatByContact("99999")
	if err == nil {
		t.Error("expected error for non-existent contact")
	}
}

// ---------- GetContactChats ----------

func TestGetContactChats(t *testing.T) {
	svc := newTestService(t, "")
	chats, err := svc.GetContactChats("11111", 10, 0)
	if err != nil {
		t.Fatalf("GetContactChats: %v", err)
	}
	if len(chats) == 0 {
		t.Fatal("expected at least one chat for sender 11111")
	}
}

// ---------- SearchContacts ----------

func TestSearchContacts_byName(t *testing.T) {
	svc := newTestService(t, "")
	contacts, err := svc.SearchContacts("Alice")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(contacts) == 0 {
		t.Fatal("expected at least one contact matching 'Alice'")
	}
	if contacts[0].Name != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %q", contacts[0].Name)
	}
}

func TestSearchContacts_byPhone(t *testing.T) {
	svc := newTestService(t, "")
	contacts, err := svc.SearchContacts("22222")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(contacts) == 0 {
		t.Fatal("expected at least one contact matching phone '22222'")
	}
}

func TestSearchContacts_withWhatsmeowContacts(t *testing.T) {
	svc := newTestServiceWithContacts(t, "")
	contacts, err := svc.SearchContacts("Charlie")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	found := false
	for _, c := range contacts {
		if c.Name == "Charlie Brown" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'Charlie Brown' from whatsmeow_contacts")
	}
}

func TestSearchContacts_excludesGroups(t *testing.T) {
	svc := newTestService(t, "")
	contacts, err := svc.SearchContacts("group")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	for _, c := range contacts {
		if c.JID == "group1@g.us" || c.JID == "group2@g.us" {
			t.Errorf("group JID %s should not appear in contacts", c.JID)
		}
	}
}

// ---------- ListMessages ----------

func TestListMessages_chronological(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Hello Alice")
	requireContains(t, result, "Family dinner tonight")
}

func TestListMessages_filteredByChat(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "group1@g.us", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Family dinner tonight")
	if containsSubstring(result, "Hello Alice") {
		t.Error("should not contain messages from other chats")
	}
}

func TestListMessages_filteredBySender(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "22222", "", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Sounds great!")
}

func TestListMessages_fts5Search(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "", "dinner", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Family dinner tonight")
}

func TestListMessages_fts5SearchNoResults(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "", "zzzznonexistent", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "No messages to display")
}

func TestListMessages_withContext(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "11111@s.whatsapp.net", "", 1, 0, true, 1, 1)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result with context")
	}
}

func TestListMessages_noMessages(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "", "nonexistent@s.whatsapp.net", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "No messages to display")
}

// ---------- GetMessageContext ----------

func TestGetMessageContext_found(t *testing.T) {
	svc := newTestService(t, "")
	ctx, err := svc.GetMessageContext("m2", 1, 1)
	if err != nil {
		t.Fatalf("GetMessageContext: %v", err)
	}
	if ctx.Message.ID != "m2" {
		t.Errorf("expected message m2, got %s", ctx.Message.ID)
	}
}

func TestGetMessageContext_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetMessageContext("nonexistent", 1, 1)
	if err == nil {
		t.Error("expected error for non-existent message")
	}
}

// ---------- GetLastInteraction ----------

func TestGetLastInteraction_found(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.GetLastInteraction("11111@s.whatsapp.net")
	if err != nil {
		t.Fatalf("GetLastInteraction: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestGetLastInteraction_notFound(t *testing.T) {
	svc := newTestService(t, "")
	_, err := svc.GetLastInteraction("nonexistent@s.whatsapp.net")
	if err == nil {
		t.Error("expected error for non-existent JID")
	}
}

// ---------- Formatting ----------

func TestFormatMessage_fromMe(t *testing.T) {
	svc := newTestService(t, "")
	msg := MCPMessage{Content: "test", IsFromMe: true, Sender: "me"}
	result := svc.formatMessage(msg)
	requireContains(t, result, "From: Me:")
}

func TestFormatMessage_withMedia(t *testing.T) {
	svc := newTestService(t, "")
	msg := MCPMessage{Content: "photo", MediaType: "image", ID: "m6", ChatJID: "test@s.whatsapp.net"}
	result := svc.formatMessage(msg)
	requireContains(t, result, "[image")
}

func TestFormatMessages_empty(t *testing.T) {
	svc := newTestService(t, "")
	result := svc.formatMessages(nil)
	if result != "No messages to display." {
		t.Errorf("expected 'No messages to display.', got %q", result)
	}
}

// ---------- Write operations (via mock HTTP) ----------

func TestSendMessage_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/send" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "sent"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, msg, err := svc.SendMessage("11111", "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !ok {
		t.Errorf("expected success, got message: %s", msg)
	}
}

func TestSendMessage_failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "not connected"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, _, err := svc.SendMessage("11111", "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if ok {
		t.Error("expected failure")
	}
}

func TestSendFile_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "file sent"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, _, err := svc.SendFile("11111", "/tmp/test.jpg")
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if !ok {
		t.Error("expected success")
	}
}

func TestSendAudioMessage_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "audio sent"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	ok, _, err := svc.SendAudioMessage("11111", "/tmp/test.ogg")
	if err != nil {
		t.Fatalf("SendAudioMessage: %v", err)
	}
	if !ok {
		t.Error("expected success")
	}
}

func TestDownloadMedia_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "ok", "path": "/tmp/media.jpg"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	path, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err != nil {
		t.Fatalf("DownloadMedia: %v", err)
	}
	if path != "/tmp/media.jpg" {
		t.Errorf("expected /tmp/media.jpg, got %s", path)
	}
}

func TestDownloadMedia_failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "message": "not found"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	_, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err == nil {
		t.Error("expected error on download failure")
	}
}

func TestSendMessage_networkError(t *testing.T) {
	svc := newTestService(t, "http://127.0.0.1:1")
	_, _, err := svc.SendMessage("11111", "hello")
	if err == nil {
		t.Error("expected error on network failure")
	}
}

func TestDownloadMedia_networkError(t *testing.T) {
	svc := newTestService(t, "http://127.0.0.1:1")
	_, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err == nil {
		t.Error("expected error on network failure")
	}
}

// ---------- Helpers ----------

func TestParseTime_formats(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"2024-01-15T10:30:00Z", true},
		{"2024-01-15 10:30:00", true},
		{"2024-01-15 10:30:00-05:00", true},
		{"invalid", false},
	}
	for _, tt := range tests {
		result := parseTime(tt.input)
		if tt.valid && result.IsZero() {
			t.Errorf("parseTime(%q) returned zero time, expected valid", tt.input)
		}
	}
}

func TestNullStr(t *testing.T) {
	valid := sql.NullString{String: "hello", Valid: true}
	if got := nullStr(valid); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	empty := sql.NullString{}
	if got := nullStr(empty); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestJidPhone(t *testing.T) {
	tests := []struct{ in, want string }{
		{"11111@s.whatsapp.net", "11111"},
		{"group@g.us", "group"},
		{"nojid", "nojid"},
	}
	for _, tt := range tests {
		if got := jidPhone(tt.in); got != tt.want {
			t.Errorf("jidPhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIntArg(t *testing.T) {
	args := map[string]interface{}{"n": float64(42), "s": "not a number"}
	if got := intArg(args, "n", 0); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := intArg(args, "missing", 99); got != 99 {
		t.Errorf("expected default 99, got %d", got)
	}
	if got := intArg(args, "s", 10); got != 10 {
		t.Errorf("expected default 10 for wrong type, got %d", got)
	}
}

func TestBoolArg(t *testing.T) {
	args := map[string]interface{}{"b": true, "s": "not bool"}
	if got := boolArg(args, "b", false); !got {
		t.Error("expected true")
	}
	if got := boolArg(args, "missing", true); !got {
		t.Error("expected default true")
	}
	if got := boolArg(args, "s", false); got {
		t.Error("expected default false for wrong type")
	}
}
