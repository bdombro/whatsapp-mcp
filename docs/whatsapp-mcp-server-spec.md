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

## Hybrid Search

When the `list_messages` tool is called with a `query` parameter, the server uses hybrid search that combines two ranking signals via Reciprocal Rank Fusion (RRF):

### BM25 Keyword Search (FTS5)

Uses SQLite's FTS5 full-text search engine with BM25 scoring. The `messages_fts` virtual table is maintained by the Go CLI via triggers, so the index is always in sync with the messages table. FTS5 provides tokenized keyword matching, implicit AND of terms, and TF-IDF-based relevance ranking. This handles exact keyword matches well.

### Semantic Vector Search

Uses [fastembed](https://github.com/qdrant/fastembed) with the `BAAI/bge-small-en-v1.5` model (33M params, 384-dimensional embeddings, ONNX inference — no PyTorch required). Embeddings are computed locally with no external API calls.

- **Model**: BAAI/bge-small-en-v1.5 (downloaded automatically on first use, ~66MB)
- **Storage**: Embeddings are cached in `~/.local/share/whatsapp-cli/search.db` as packed float32 BLOBs
- **Incremental**: Only unembedded messages are processed on each search; previously computed embeddings are reused
- **Latency**: First search embeds all messages (~5K messages in ~60s); subsequent searches run in ~16ms with warm model

### Reciprocal Rank Fusion (RRF)

Both BM25 and vector search produce independently ranked result lists. RRF merges them using:

```
score(d) = Σ 1/(k + rank_i(d))
```

where `k=60` (standard constant) and `rank_i(d)` is the rank of document `d` in result list `i`. Documents appearing in both lists get boosted; documents appearing in only one list are still included. This approach is parameter-free and robust across different score distributions.

### Embedding Pre-computation

During `whatsapp-cli login`, the CLI shells out to `uv run search.py` to pre-compute embeddings for all existing messages. This eliminates the ~60-second cold start that would otherwise occur on the first MCP search query. The step is best-effort — it is silently skipped if `uv` or the MCP server is not installed.

### Fallback Behavior

- Without a `query` parameter, `list_messages` falls back to chronological listing with optional filters (no search ranking)
- If the FTS5 table doesn't exist (old Go binary without FTS5 support), BM25 search silently returns no results and vector search carries the full weight
- If embeddings haven't been computed yet, they are computed synchronously on first search

## Fuzzy Chat & Participant Search

The `list_chats` tool supports fuzzy search across two dimensions when a query is provided:

1. **Chat name matching** — all chat names are loaded and compared against the query using case-insensitive substring matching followed by word-level fuzzy matching (Python `difflib.SequenceMatcher` with a 0.6 similarity threshold). This handles typos like "famly" matching "Family" and partial words like "birth" matching "Birthday Group".

2. **Participant name matching** — the `whatsmeow_contacts` table in `whatsapp.db` is searched for contacts whose `full_name` or `push_name` fuzzy-matches the query. Matching contact JIDs are then looked up in the `group_participants` table to find groups they belong to, plus their direct chat JIDs. This means searching for "Kevin" returns Kevin's direct chat and also any group where Kevin is a member, even if the group name doesn't contain "Kevin".

For queries shorter than 3 characters, only exact substring matching is used (fuzzy word matching is disabled to avoid false positives). Multi-word queries require each word to fuzzy-match at least one word in the target text.

## MCP Tools

### Contact & Chat Discovery

| Tool | Description |
|------|-------------|
| `search_contacts` | Search contacts by name or phone number. Queries both the local `chats` table and `whatsmeow_contacts` in `whatsapp.db` for broader coverage. Excludes group JIDs. |
| `list_chats` | List chats with optional fuzzy search by chat name or participant name. When a query is provided, uses case-insensitive substring matching plus word-level similarity (via `SequenceMatcher`, threshold 0.6) on chat names, and also searches `whatsmeow_contacts` for matching participant names to find groups where that person is a member. |
| `get_chat` | Get a single chat by JID with optional last message. |
| `get_direct_chat_by_contact` | Find the direct (non-group) chat for a given phone number. |
| `get_contact_chats` | List all chats (including groups) where a contact appears as sender. |

### Message Reading

| Tool | Description |
|------|-------------|
| `list_messages` | Search and filter messages by time range, sender, chat JID, or text content. When a query is provided, uses hybrid search (BM25 + vector similarity) for relevance-ranked results. Supports pagination and optional surrounding context per message. |
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
| `whatsapp.py` | Core logic: SQLite queries, HTTP calls to the CLI, data models (`Message`, `Chat`, `Contact`, `MessageContext`), message formatting. Delegates search queries to `search.py`. |
| `search.py` | Hybrid search module: FTS5 BM25 search, fastembed vector search, embedding cache management, and Reciprocal Rank Fusion scoring. |
| `audio.py` | ffmpeg wrapper for converting audio files to ogg/opus format for voice messages. |

## Configuration

No environment variables. Paths and API URL are hardcoded:

| Setting | Value |
|---------|-------|
| Messages database | `~/.local/share/whatsapp-cli/messages.db` |
| Embedding cache | `~/.local/share/whatsapp-cli/search.db` |
| Embedding model | `BAAI/bge-small-en-v1.5` (384 dimensions) |
| Bridge API | `http://localhost:8080/api` |

## Dependencies

Defined in `pyproject.toml`:

- `mcp[cli]` — MCP Python SDK with FastMCP
- `requests` — HTTP client for CLI API calls
- `httpx` — listed but currently unused in application code
- `fastembed` — ONNX-based text embedding (BAAI/bge-small-en-v1.5)
- `numpy` — Vector operations for cosine similarity
- Python 3.11+
- **ffmpeg** (optional) — required only for automatic audio format conversion in `send_audio_message`
