package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func runMcp() {
	store, err := NewMessageStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open message store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	svc := NewMCPService(store, "http://localhost:8080/api")

	s := server.NewMCPServer(
		"whatsapp",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	registerMCPTools(s, svc)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

func registerMCPTools(s *server.MCPServer, svc *MCPService) {
	// ---- Contact & Chat Discovery ----

	s.AddTool(mcp.NewTool("search_contacts",
		mcp.WithDescription("Search WhatsApp contacts by name or phone number."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search term to match against contact names or phone numbers")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, _ := req.RequireString("query")
		contacts, err := svc.SearchContacts(q)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(contacts)
	})

	s.AddTool(mcp.NewTool("list_chats",
		mcp.WithDescription("Get WhatsApp chats matching specified criteria. Supports fuzzy search by chat name or participant name."),
		mcp.WithString("query", mcp.Description("Optional search term to filter chats by name, JID, or participant name")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of chats to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
		mcp.WithBoolean("include_last_message", mcp.Description("Whether to include the last message in each chat (default true)")),
		mcp.WithString("sort_by", mcp.Description("Sort by 'last_active' or 'name' (default 'last_active')")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := args["query"].(string)
		limit := intArg(args, "limit", 20)
		page := intArg(args, "page", 0)
		includeLast := boolArg(args, "include_last_message", true)
		sortBy, _ := args["sort_by"].(string)
		if sortBy == "" {
			sortBy = "last_active"
		}
		chats, err := svc.ListChats(query, limit, page, includeLast, sortBy)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chats)
	})

	s.AddTool(mcp.NewTool("get_chat",
		mcp.WithDescription("Get WhatsApp chat metadata by JID."),
		mcp.WithString("chat_jid", mcp.Required(), mcp.Description("The JID of the chat to retrieve")),
		mcp.WithBoolean("include_last_message", mcp.Description("Whether to include the last message (default true)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, _ := req.RequireString("chat_jid")
		includeLast := boolArg(req.GetArguments(), "include_last_message", true)
		chat, err := svc.GetChat(jid, includeLast)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chat)
	})

	s.AddTool(mcp.NewTool("get_direct_chat_by_contact",
		mcp.WithDescription("Get WhatsApp chat metadata by sender phone number."),
		mcp.WithString("sender_phone_number", mcp.Required(), mcp.Description("The phone number to search for")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		phone, _ := req.RequireString("sender_phone_number")
		chat, err := svc.GetDirectChatByContact(phone)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chat)
	})

	s.AddTool(mcp.NewTool("get_contact_chats",
		mcp.WithDescription("Get all WhatsApp chats involving the contact."),
		mcp.WithString("jid", mcp.Required(), mcp.Description("The contact's JID to search for")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of chats to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, _ := req.RequireString("jid")
		args := req.GetArguments()
		limit := intArg(args, "limit", 20)
		page := intArg(args, "page", 0)
		chats, err := svc.GetContactChats(jid, limit, page)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(chats)
	})

	// ---- Message Reading ----

	s.AddTool(mcp.NewTool("list_messages",
		mcp.WithDescription("Get WhatsApp messages matching specified criteria with optional context. When a query is provided, uses BM25 keyword search for relevance-ranked results."),
		mcp.WithString("after", mcp.Description("Optional ISO-8601 date to only return messages after")),
		mcp.WithString("before", mcp.Description("Optional ISO-8601 date to only return messages before")),
		mcp.WithString("sender_phone_number", mcp.Description("Optional phone number to filter by sender")),
		mcp.WithString("chat_jid", mcp.Description("Optional chat JID to filter by chat")),
		mcp.WithString("query", mcp.Description("Optional search term to filter messages by content")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of messages to return (default 20)")),
		mcp.WithNumber("page", mcp.Description("Page number for pagination (default 0)")),
		mcp.WithBoolean("include_context", mcp.Description("Include messages before and after matches (default true)")),
		mcp.WithNumber("context_before", mcp.Description("Number of messages to include before each match (default 1)")),
		mcp.WithNumber("context_after", mcp.Description("Number of messages to include after each match (default 1)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		after, _ := args["after"].(string)
		before, _ := args["before"].(string)
		sender, _ := args["sender_phone_number"].(string)
		chatJID, _ := args["chat_jid"].(string)
		query, _ := args["query"].(string)
		limit := intArg(args, "limit", 20)
		page := intArg(args, "page", 0)
		includeCtx := boolArg(args, "include_context", true)
		ctxBefore := intArg(args, "context_before", 1)
		ctxAfter := intArg(args, "context_after", 1)
		result, err := svc.ListMessages(after, before, sender, chatJID, query, limit, page, includeCtx, ctxBefore, ctxAfter)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("get_message_context",
		mcp.WithDescription("Get context around a specific WhatsApp message."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The ID of the message to get context for")),
		mcp.WithNumber("before", mcp.Description("Number of messages before (default 5)")),
		mcp.WithNumber("after", mcp.Description("Number of messages after (default 5)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msgID, _ := req.RequireString("message_id")
		args := req.GetArguments()
		before := intArg(args, "before", 5)
		after := intArg(args, "after", 5)
		result, err := svc.GetMessageContext(msgID, before, after)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	})

	s.AddTool(mcp.NewTool("get_last_interaction",
		mcp.WithDescription("Get most recent WhatsApp message involving the contact."),
		mcp.WithString("jid", mcp.Required(), mcp.Description("The JID of the contact")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		jid, _ := req.RequireString("jid")
		result, err := svc.GetLastInteraction(jid)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	// ---- Sending ----

	s.AddTool(mcp.NewTool("send_message",
		mcp.WithDescription("Send a WhatsApp message to a person or group. For group chats use the JID."),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("message", mcp.Required(), mcp.Description("The message text to send")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, _ := req.RequireString("recipient")
		message, _ := req.RequireString("message")
		success, msg, err := svc.SendMessage(recipient, message)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	s.AddTool(mcp.NewTool("send_file",
		mcp.WithDescription("Send a file via WhatsApp. For group messages use the JID."),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("media_path", mcp.Required(), mcp.Description("Absolute path to the file to send")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, _ := req.RequireString("recipient")
		path, _ := req.RequireString("media_path")
		success, msg, err := svc.SendFile(recipient, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	s.AddTool(mcp.NewTool("send_audio_message",
		mcp.WithDescription("Send an audio file as a WhatsApp voice message. Non-ogg files require ffmpeg for conversion."),
		mcp.WithString("recipient", mcp.Required(), mcp.Description("Phone number with country code (no +) or JID")),
		mcp.WithString("media_path", mcp.Required(), mcp.Description("Absolute path to the audio file")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		recipient, _ := req.RequireString("recipient")
		path, _ := req.RequireString("media_path")
		success, msg, err := svc.SendAudioMessage(recipient, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": success, "message": msg})
	})

	// ---- Media ----

	s.AddTool(mcp.NewTool("download_media",
		mcp.WithDescription("Download media from a WhatsApp message and get the local file path."),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The ID of the message containing the media")),
		mcp.WithString("chat_jid", mcp.Required(), mcp.Description("The JID of the chat containing the message")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msgID, _ := req.RequireString("message_id")
		chatJID, _ := req.RequireString("chat_jid")
		path, err := svc.DownloadMedia(msgID, chatJID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"success": true, "message": "Media downloaded successfully", "file_path": path})
	})
}

// ---------- Helpers ----------

func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func intArg(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

func boolArg(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}
