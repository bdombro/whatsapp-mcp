package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// buildTestMCPServer creates a fully wired MCP server with an in-memory store
// and a mock HTTP backend for write operations.
func buildTestMCPServer(t *testing.T) (*server.MCPServer, *httptest.Server) {
	t.Helper()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/send":
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "sent"})
		case "/download":
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "ok", "path": "/tmp/test.jpg"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(apiSrv.Close)

	store := newTestStoreWithContacts(t)
	ws := NewWhatsAppSourceFromStore(store, apiSrv.URL)

	s := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(false))
	ws.RegisterTools(s)
	return s, apiSrv
}

// callTool invokes a tool by name on the MCP server and returns the text result.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) string {
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

	result := s.HandleMessage(context.Background(), mustMarshalToolCall(name, args))
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return extractText(t, data)
}

func mustMarshalToolCall(name string, args map[string]interface{}) json.RawMessage {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": name, "arguments": args},
	}
	data, _ := json.Marshal(msg)
	return data
}

func extractText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, string(raw))
	}
	if len(resp.Result.Content) == 0 {
		return ""
	}
	return resp.Result.Content[0].Text
}

// ---------- E2E Tool Tests ----------

func TestMCP_SearchContacts(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_search_contacts", map[string]interface{}{"query": "Alice"})
	requireContains(t, text, "Alice")
}

func TestMCP_ListChats(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_chats", map[string]interface{}{})
	requireContains(t, text, "group1@g.us")
}

func TestMCP_ListChats_fuzzyQuery(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_chats", map[string]interface{}{"query": "Famly"})
	requireContains(t, text, "Family Chat")
}

func TestMCP_ListChats_participantSearch(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_chats", map[string]interface{}{"query": "Charlie"})
	requireContains(t, text, "group2@g.us")
}

func TestMCP_GetChat(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_chat", map[string]interface{}{"chat_jid": "group1@g.us"})
	requireContains(t, text, "Family Chat")
}

func TestMCP_GetChat_notFound(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_chat", map[string]interface{}{"chat_jid": "nonexistent@s.whatsapp.net"})
	requireContains(t, text, "not found")
}

func TestMCP_GetDirectChatByContact(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_direct_chat_by_contact", map[string]interface{}{"sender_phone_number": "11111"})
	requireContains(t, text, "Alice Smith")
}

func TestMCP_GetContactChats(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_contact_chats", map[string]interface{}{"jid": "11111"})
	requireContains(t, text, "11111@s.whatsapp.net")
}

func TestMCP_ListMessages(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_messages", map[string]interface{}{"chat_jid": "group1@g.us"})
	requireContains(t, text, "dinner")
}

func TestMCP_ListMessages_search(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_list_messages", map[string]interface{}{"query": "meeting"})
	requireContains(t, text, "Meeting at 3pm")
}

func TestMCP_GetMessageContext(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_message_context", map[string]interface{}{"message_id": "m4"})
	requireContains(t, text, "m4")
}

func TestMCP_GetLastInteraction(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_get_last_interaction", map[string]interface{}{"jid": "11111@s.whatsapp.net"})
	if text == "" {
		t.Error("expected non-empty last interaction")
	}
}

func TestMCP_SendMessage(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_send_message", map[string]interface{}{"recipient": "11111", "message": "hi"})
	requireContains(t, text, "success")
}

func TestMCP_SendFile(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_send_file", map[string]interface{}{"recipient": "11111", "media_path": "/tmp/test.jpg"})
	requireContains(t, text, "success")
}

func TestMCP_SendAudioMessage(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_send_audio_message", map[string]interface{}{"recipient": "11111", "media_path": "/tmp/test.ogg"})
	requireContains(t, text, "success")
}

func TestMCP_DownloadMedia(t *testing.T) {
	s, _ := buildTestMCPServer(t)
	text := callTool(t, s, "whatsapp_download_media", map[string]interface{}{"message_id": "m6", "chat_jid": "22222@s.whatsapp.net"})
	requireContains(t, text, "file_path")
}

// ---------- Tool listing ----------

func TestMCP_ToolsListContainsAllTools(t *testing.T) {
	s, _ := buildTestMCPServer(t)

	listMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	initMsg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	resultIface := s.HandleMessage(context.Background(), listMsg)
	result, _ := json.Marshal(resultIface)

	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	json.Unmarshal(result, &resp)

	expectedTools := []string{
		"whatsapp_search_contacts", "whatsapp_list_chats", "whatsapp_get_chat",
		"whatsapp_get_direct_chat_by_contact", "whatsapp_get_contact_chats",
		"whatsapp_list_messages", "whatsapp_get_message_context",
		"whatsapp_get_last_interaction", "whatsapp_send_message", "whatsapp_send_file",
		"whatsapp_send_audio_message", "whatsapp_download_media",
	}

	toolNames := make(map[string]bool)
	for _, tool := range resp.Result.Tools {
		toolNames[tool.Name] = true
	}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

// ---------- DataSource interface ----------

func TestWhatsAppSource_interface(t *testing.T) {
	store := newTestStore(t)
	ws := NewWhatsAppSourceFromStore(store, "http://localhost:1")
	defer ws.Close()

	if ws.Name() != "whatsapp" {
		t.Errorf("expected name 'whatsapp', got %q", ws.Name())
	}
	if ws.Description() != "WhatsApp" {
		t.Errorf("expected description 'WhatsApp', got %q", ws.Description())
	}

	var _ DataSource = ws
}
