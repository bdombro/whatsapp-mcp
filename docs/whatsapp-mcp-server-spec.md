# WhatsApp MCP Server - Product Spec

The MCP server is a Python application that implements the [Model Context Protocol](https://modelcontextprotocol.io/) for WhatsApp. It allows AI assistants (Claude Desktop, Cursor) to search, read, and send WhatsApp messages through a standardized tool interface.

## Usage

The server is launched by the MCP host (Claude Desktop or Cursor) via the configured command. It communicates over **stdio** using the MCP protocol.

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "<path-to-uv>",
      "args": ["--directory", "<path-to-repo>/whatsapp-mcp-server", "run", "main.py"]
    }
  }
}
```

For **Claude Desktop**: save to `~/Library/Application Support/Claude/claude_desktop_config.json`
For **Cursor**: save to `~/.cursor/mcp.json`

## Architecture

The server uses [FastMCP](https://github.com/modelcontextprotocol/python-sdk) as the framework. Communication with the MCP host is over stdio (not HTTP).

**Read path**: The server queries `messages.db` directly via SQLite for all read operations (message search, chat listing, contact lookup). This means the Go CLI does not need to be running for read-only operations.

**Write path**: Sending messages and downloading media go through the Go CLI's REST API at `http://localhost:8080/api`. The CLI must be running for these operations.

## MCP Tools

### Contact & Chat Discovery

| Tool | Description |
|------|-------------|
| `search_contacts` | Search contacts by name or phone number. Excludes group JIDs. |
| `list_chats` | List chats with optional name/JID filter, last message preview, and sort order (`last_active` or `name`). |
| `get_chat` | Get a single chat by JID with optional last message. |
| `get_direct_chat_by_contact` | Find the direct (non-group) chat for a given phone number. |
| `get_contact_chats` | List all chats (including groups) where a contact appears as sender. |

### Message Reading

| Tool | Description |
|------|-------------|
| `list_messages` | Search and filter messages by time range, sender, chat JID, or text content. Supports pagination and optional surrounding context per message. |
| `get_message_context` | Get messages before and after a specific message ID within the same chat. |
| `get_last_interaction` | Get the most recent message involving a contact, returned as a formatted string. |

### Sending

| Tool | Description |
|------|-------------|
| `send_message` | Send a text message to a phone number or group JID. Routes through the CLI's `/api/send` endpoint. |
| `send_file` | Send a local file (image, video, document) as a media message. The file must be accessible on the machine running the server. |
| `send_audio_message` | Send an audio file as a playable WhatsApp voice message. Non-ogg files are automatically converted to ogg/opus via ffmpeg. |

### Media

| Tool | Description |
|------|-------------|
| `download_media` | Download media from a received message by `message_id` and `chat_jid`. Routes through the CLI's `/api/download` endpoint. Returns the local file path. |

## Files

| File | Purpose |
|------|---------|
| `main.py` | FastMCP server entry point. Defines all `@mcp.tool()` decorated functions that delegate to `whatsapp.py`. |
| `whatsapp.py` | Core logic: SQLite queries, HTTP calls to the CLI, data models (`Message`, `Chat`, `Contact`, `MessageContext`), message formatting. |
| `audio.py` | ffmpeg wrapper for converting audio files to ogg/opus format for voice messages. |

## Configuration

No environment variables. Paths and API URL are hardcoded:

| Setting | Value |
|---------|-------|
| Database path | `~/.local/share/whatsapp-cli/messages.db` |
| Bridge API | `http://localhost:8080/api` |

## Dependencies

Defined in `pyproject.toml`:

- `mcp[cli]` — MCP Python SDK with FastMCP
- `requests` — HTTP client for CLI API calls
- `httpx` — listed but currently unused in application code
- Python 3.11+
- **ffmpeg** (optional) — required only for automatic audio format conversion in `send_audio_message`
