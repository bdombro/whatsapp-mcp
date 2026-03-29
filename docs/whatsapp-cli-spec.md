# WhatsApp CLI - Product Spec

The WhatsApp CLI is a Go application that connects to WhatsApp's web multidevice API via the [whatsmeow](https://github.com/tulir/whatsmeow) library. It authenticates as a linked device, receives messages in real time, syncs history, stores everything in a local SQLite database, and exposes a REST API for sending messages and downloading media.

## Building

```
cd whatsapp-cli
go build -o whatsapp-cli .
```

CGO must be enabled (default on macOS/Linux) since `go-sqlite3` requires it. On Windows, install a C compiler via [MSYS2](https://www.msys2.org/) and run `go env -w CGO_ENABLED=1` first.

## Usage

### Login

```
whatsapp-cli login
whatsapp-cli login --relogin
```

Authenticates with WhatsApp by displaying a QR code. Scan it with your phone (Settings > Linked Devices). If already logged in, shows account info. Required before running `core` or `install-daemon`.

During first login, the CLI captures WhatsApp's initial history sync and stores it in `messages.db`. This is the only time WhatsApp pushes the full chat history. After history sync completes, the CLI pre-computes vector embeddings for the MCP server's hybrid search index so the first query doesn't pay the cold-start cost (~60 seconds for ~5K messages). This step is best-effort and skipped if `uv` or the MCP server is not installed.

The `--relogin` flag clears the existing session and message databases, re-displays the QR code for a fresh pairing, captures the initial history sync, rebuilds the search index, and restarts the core daemon if it was previously running. Use this when the session is stale or when the initial history sync was missed.

### Core — WhatsApp Connection

```
whatsapp-cli core
```

Connects to WhatsApp, listens for messages, syncs history, and starts the REST API server. Requires login. Re-authentication may be required after ~20 days.

### Daemon Management

```
whatsapp-cli install-daemon
whatsapp-cli start
whatsapp-cli stop
whatsapp-cli restart
```

| Command | Description |
|---------|-------------|
| `install-daemon` | Installs and starts the core daemon as a macOS LaunchAgent (`com.whatsapp-cli.core`). Runs on login and auto-restarts on crash. Requires login. Logs to `~/.local/share/whatsapp-cli/core.log`. |
| `start` | Starts the core daemon. |
| `stop` | Stops the core daemon. |
| `restart` | Restarts the core daemon (stop + start). |

### Maintenance

```
whatsapp-cli info
whatsapp-cli reset
whatsapp-cli uninstall
```

| Command | Description |
|---------|-------------|
| `info` | Shows data directory, logged-in WhatsApp account, message database stats, and daemon install status. |
| `reset` | Uninstalls the daemon, then wipes all local data including databases, logs, and the WhatsApp session. |
| `uninstall` | Resets everything, removes installed binaries from `/usr/local/bin`, and cleans shell completions from `~/.zshrc`. |

### Shell Completions

```
whatsapp-cli completions bash
whatsapp-cli completions zsh
```

Add to your shell profile (e.g. `~/.bashrc` or `~/.zshrc`):

```bash
eval "$(whatsapp-cli completions zsh)"
```

The core daemon runs as `com.whatsapp-cli.core` via launchd. Logs go to `~/.local/share/whatsapp-cli/core.log`.

---

## WhatsApp Connection

- Connects via [whatsmeow](https://github.com/tulir/whatsmeow) as a linked companion device
- Handles QR code pairing flow (3-minute timeout)
- Automatically reconnects on subsequent runs using session stored in `whatsapp.db`
- Listens for real-time message events and history sync events

### How whatsmeow Works

whatsmeow is an unofficial Go library that implements the WhatsApp Web multidevice protocol. It connects as a "linked device" — the same mechanism WhatsApp Web and WhatsApp Desktop use. Key concepts:

- **Session store** (`whatsapp.db`) — whatsmeow persists device credentials, encryption keys, contact data, and LID (Linked Identity) mappings in a SQLite database. The CLI uses whatsmeow's built-in `sqlstore` driver.
- **Event-driven** — the CLI registers event handlers on whatsmeow's `Client`. Incoming messages arrive as `events.Message`, history sync batches arrive as `events.HistorySync`, and connection status changes arrive as `events.Connected`, `events.Disconnected`, etc.
- **Protobuf wire format** — WhatsApp messages are defined as Protocol Buffer messages. whatsmeow decodes them into Go structs (e.g. `waProto.Message`, `waProto.WebMessageInfo`, `waProto.Conversation`). The CLI extracts text content, media metadata, sender info, and group participant lists from these protobufs.
- **Contact and LID databases** — whatsmeow automatically processes push names and phone-to-LID mappings that arrive in history sync payloads and stores them in `whatsmeow_contacts` and `whatsmeow_lid_map` tables within `whatsapp.db`. The CLI reads these tables for name resolution but never writes to them directly.

### History Sync

When WhatsApp pushes historical conversations (on first connect or periodically), the CLI processes each conversation to maximise the amount of data captured:

1. Resolves the chat name (group name or contact name)
2. Extracts group participants directly from conversation metadata (the `Participant` field on the `Conversation` proto) and stores them in the `group_participants` table. This provides participant data even for groups the user has since left.
3. Extracts message content and media metadata. Non-text message types (stickers, contacts, locations, polls, reactions, etc.) are stored with descriptive placeholder text instead of being silently dropped. Supported content types:
   - Plain text and extended text are stored verbatim
   - Media (image, video, audio, document) stores metadata (type, filename, URL, encryption keys)
   - Stickers, contacts, locations, group invites, polls, reactions, lists, buttons, view-once, and ephemeral messages are all captured with descriptive text
4. Determines the message sender using multiple fallback fields: `Key.Participant`, `WebMessageInfo.Participant`, `PushName`, and finally the chat JID. WhatsApp populates these fields inconsistently, so checking all of them maximises sender attribution.
5. Stores each message with sender, timestamp, and media info
6. Requests up to 500 messages per on-demand history sync via `SendPeerMessage`

Push names (display names) and phone-to-LID mappings included in the history sync payload are processed automatically by whatsmeow and stored in the contacts database for later name resolution.

### WhatsApp API Limitations

WhatsApp's servers and the multidevice protocol have several known limitations that affect data completeness:

**Group names** — The `GetJoinedGroups` and `GetGroupInfo` APIs return participant lists for most groups but omit the group name (`Subject` field) for a significant fraction (~40%) of groups. This appears to be a server-side limitation that varies by group type, creation date, or privacy settings.

**Group sender attribution** — History sync messages in group chats frequently omit the individual sender. The `Key.Participant` field (which should identify who sent the message) is often nil. A separate `WebMessageInfo.Participant` field and the `PushName` field sometimes carry this data, but many group messages arrive with no sender attribution at all.

**Groups the user has left** — `GetJoinedGroups` only returns currently joined groups. For groups the user has since left, individual `GetGroupInfo` calls may fail with 401/404 errors if the group no longer allows access. The CLI mitigates this by extracting participants from history sync conversation metadata, which is available regardless of current group membership.

**History sync completeness** — WhatsApp controls how much history it pushes to linked devices. The initial sync typically delivers recent messages (days to weeks), not full history. The CLI requests up to 500 messages per on-demand sync but the server may deliver fewer. There is no API to request messages older than what the server chooses to provide.

**Linked IDs (LIDs)** — WhatsApp uses opaque Linked IDs (`number@lid`) internally. The `whatsmeow_lid_map` table maps LIDs to phone numbers, but this mapping is only populated for contacts encountered during the session. Some LIDs may never resolve to a phone number if the contact was never seen in a push name or history sync event.

**Contact names** — The `whatsmeow_contacts` table stores names as either `full_name` (from the user's address book, synced from the phone) or `push_name` (the name the contact has set for themselves). Address book names are only available if the phone syncs contacts to WhatsApp. Push names may change and only the most recent value is stored.

### Media Handling

**Incoming media** — metadata (type, filename, URL, encryption keys, SHA256 hashes, file length) is extracted and stored alongside the message. Supported types: image (jpg, png, gif, webp), video (mp4, avi, mov), audio (ogg/opus), and documents.

**Sending media** — the CLI reads the local file, determines MIME type from extension, uploads to WhatsApp servers, and sends the appropriate message type (ImageMessage, AudioMessage, VideoMessage, DocumentMessage). Audio files in ogg/opus format are sent as voice messages with duration and waveform metadata.

**Downloading media** — reconstructs download parameters from stored metadata, downloads via whatsmeow, and saves to `~/.local/share/whatsapp-cli/{chat_jid}/`. Returns the absolute file path. Files are cached so repeated downloads are a no-op.

## REST API

The CLI starts an HTTP server on port **8080** with two endpoints:

### POST /api/send

Send a text message or media file to a recipient.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `recipient` | string | Yes | Phone number or full JID (e.g. `number@s.whatsapp.net`, `number@g.us`) |
| `message` | string | * | Text content or caption for media |
| `media_path` | string | * | Local file path to send as media |

\* At least one of `message` or `media_path` must be provided.

**Response:** `{ "success": bool, "message": string }`

### POST /api/download

Download media from a previously received message.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `message_id` | string | Yes | Message ID |
| `chat_jid` | string | Yes | Chat JID the message belongs to |

**Response:** `{ "success": bool, "message": string, "filename": string, "path": string }`

---

## Name Resolution

Contact names are resolved through a multi-step lookup, falling through until a non-phone-number name is found:

1. **messages.db `chats` table** — exact JID match
2. **messages.db `chats` table** — LIKE match on the phone number portion
3. **whatsapp.db `whatsmeow_contacts`** — lookup by full JID, `phone@s.whatsapp.net`, or `phone@lid`; returns `full_name` or `push_name`
4. **whatsapp.db LID mapping** — for LID-based senders, maps LID to phone via `whatsmeow_lid_map`, then re-looks up in `whatsmeow_contacts`
5. **Fallback** — raw sender string (phone number or JID)

At each step, results that look like phone numbers (all digits, optional leading `+`) or group placeholder names (`Group 120363...`) are skipped in favour of a more authoritative source. If the stored name is a placeholder and a real name is resolved, the `chats` table is automatically updated. For LID-based senders where no contact name exists, the resolved phone number is returned instead of the opaque LID. See [WhatsApp API Limitations](#whatsapp-api-limitations) for cases where resolution cannot succeed.

## Data Storage

All data is stored in `~/.local/share/whatsapp-cli/`.

| Path | Purpose |
|------|---------|
| `whatsapp.db` | whatsmeow session store (device credentials, contacts, LID mappings) |
| `messages.db` | Application message and chat database (includes FTS5 index) |
| `search.db` | Vector embedding cache for semantic search (managed by MCP server) |
| `{chat_jid}/` | Downloaded media files, organized by chat |
| `core.log` | Core daemon log |

Both databases are opened by `NewMessageStore()`. The contacts DB is optional and non-fatal if missing. Both databases use WAL journal mode and a 5-second busy timeout to allow concurrent access from the core daemon without locking conflicts.

### Database Schema

| Table | Key | Contents |
|-------|-----|----------|
| `chats` | `jid` (primary) | Chat JID, display name, last message timestamp |
| `messages` | `(id, chat_jid)` (composite) | Sender, content, timestamp, `is_from_me`, media metadata (type, filename, URL, encryption keys) |
| `group_participants` | `(group_jid, participant_jid)` (composite) | Maps each group chat to its individual member JIDs, extracted from history sync conversation metadata and WhatsApp's `GetGroupInfo` API |
| `messages_fts` | (FTS5 virtual) | Full-text search index on `messages.content`, maintained via triggers |

Tables are created on startup if they don't exist. The FTS5 index is automatically rebuilt from the messages table on first run. The Go binary must be built with `-tags "sqlite_fts5"` to enable FTS5 support.

### Full-Text Search (FTS5)

The `messages_fts` table is an external-content FTS5 virtual table backed by the `messages` table. It enables BM25-ranked keyword search over message content. Three triggers (`messages_fts_insert`, `messages_fts_delete`, `messages_fts_update`) keep the index in sync as messages are added or modified. On startup, if the index is empty but messages exist, a full rebuild is performed automatically.

## Dependencies

- [whatsmeow](https://github.com/tulir/whatsmeow) — WhatsApp web multidevice API
- [go-sqlite3](https://github.com/mattn/go-sqlite3) — SQLite driver (requires CGO, built with `sqlite_fts5` tag)
- [qrterminal](https://github.com/mdp/qrterminal) — QR code rendering in terminal
