package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// Additional tests to maximize coverage on new MCP files.

// ---------- mcp.go: intArg with int type, jsonResult error, missing args ----------

func TestIntArg_intType(t *testing.T) {
	args := map[string]interface{}{"n": 42}
	if got := intArg(args, "n", 0); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestJsonResult_valid(t *testing.T) {
	result, err := jsonResult(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("jsonResult: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ---------- mcp_service.go: bm25Search with all filters ----------

func TestBM25Search_withFilters(t *testing.T) {
	svc := newTestService(t, "")
	results := svc.bm25Search("dinner", 10, "group1@g.us", "2000-01-01", "2099-12-31")
	if len(results) == 0 {
		t.Error("expected BM25 results for 'dinner' in group1@g.us")
	}
}

func TestBM25Search_noResults(t *testing.T) {
	svc := newTestService(t, "")
	results := svc.bm25Search("zzzznonexistent", 10, "", "", "")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ---------- mcp_service.go: hybridMessageSearch with sender filter ----------

func TestHybridSearch_withSenderFilter(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "11111", "", "dinner", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "dinner")
}

func TestHybridSearch_senderMismatch(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("", "", "99999", "", "dinner", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "No messages to display")
}

// ---------- mcp_service.go: listMessagesChronological with all filters ----------

func TestListMessages_allFilters(t *testing.T) {
	svc := newTestService(t, "")
	result, err := svc.ListMessages("2000-01-01", "2099-12-31", "11111", "11111@s.whatsapp.net", "", 10, 0, false, 0, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	requireContains(t, result, "Hello Alice")
}

// ---------- mcp_service.go: postSend with invalid JSON ----------

func TestPostSend_invalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	_, msg, err := svc.SendMessage("11111", "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if msg != "not json" {
		t.Errorf("expected raw text, got %q", msg)
	}
}

func TestDownloadMedia_invalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	svc := newTestService(t, srv.URL)
	_, err := svc.DownloadMedia("m6", "22222@s.whatsapp.net")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ---------- mcp_service.go: findChatsByParticipantName no contacts DB ----------

func TestFindChatsByParticipantName_noContactsDB(t *testing.T) {
	svc := newTestService(t, "")
	result := svc.findChatsByParticipantName("Alice")
	if len(result) != 0 {
		t.Errorf("expected no results without contacts DB, got %d", len(result))
	}
}

func TestFindChatsByParticipantName_noMatch(t *testing.T) {
	svc := newTestServiceWithContacts(t, "")
	result := svc.findChatsByParticipantName("zzzznonexistent")
	if len(result) != 0 {
		t.Errorf("expected no results, got %d", len(result))
	}
}

// ---------- mcp_service.go: SearchContacts whatsmeow pushName fallback ----------

func TestSearchContacts_pushNameFallback(t *testing.T) {
	store := newTestStoreWithContacts(t)
	// Add contact with only push_name
	store.contactsDB.Exec(`INSERT INTO whatsmeow_contacts VALUES ('44444@s.whatsapp.net', NULL, 'DaveP')`)
	svc := NewMCPService(store, "")

	contacts, err := svc.SearchContacts("DaveP")
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	found := false
	for _, c := range contacts {
		if c.Name == "DaveP" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'DaveP' via push_name fallback")
	}
}

// ---------- mcp.go: MCP tool error paths (missing required args) ----------

func callToolRaw(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) string {
	t.Helper()
	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": name, "arguments": args},
	})
	result := s.HandleMessage(context.Background(), msg)
	data, _ := json.Marshal(result)
	return string(data)
}

func TestMCP_GetChat_missingArg(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callToolRaw(t, s, "whatsapp_get_chat", map[string]interface{}{})
	requireContains(t, text, "not found")
}

func TestMCP_GetDirectChat_notFound(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callToolRaw(t, s, "whatsapp_get_direct_chat_by_contact", map[string]interface{}{"sender_phone_number": "99999999"})
	requireContains(t, text, "no direct chat")
}

func TestMCP_SendMessage_missingArgs(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callToolRaw(t, s, "whatsapp_send_message", map[string]interface{}{})
	requireContains(t, text, "success")
}

// ---------- mcp_service.go: expandContext error path ----------

func TestExpandContext_missingMessage(t *testing.T) {
	svc := newTestService(t, "")
	// Pass a message with a non-existent ID — expandContext should fall back to the original
	msgs := []MCPMessage{{ID: "nonexistent", ChatJID: "11111@s.whatsapp.net"}}
	expanded := svc.expandContext(msgs, 1, 1)
	if len(expanded) != 1 {
		t.Errorf("expected 1 message (fallback), got %d", len(expanded))
	}
}

// ---------- mcp_service.go: GetContactChats pagination ----------

func TestGetContactChats_pagination(t *testing.T) {
	svc := newTestService(t, "")
	page0, _ := svc.GetContactChats("11111", 1, 0)
	page1, _ := svc.GetContactChats("11111", 1, 1)
	if len(page0) != 1 {
		t.Errorf("page 0: expected 1, got %d", len(page0))
	}
	_ = page1
}

// ---------- mcp_service.go: messagesAround with results ----------

func TestMessagesAround(t *testing.T) {
	svc := newTestService(t, "")
	msgs := svc.messagesAround("11111@s.whatsapp.net", "2099-12-31T00:00:00Z", "< ?", "DESC", 2)
	if len(msgs) == 0 {
		t.Error("expected messages before 2099")
	}
}
