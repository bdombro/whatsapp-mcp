# WhatsApp CLI - Product Spec

The WhatsApp CLI is a Go application that connects to WhatsApp's web multidevice API via the [whatsmeow](https://github.com/tulir/whatsmeow) library. It authenticates as a linked device, receives messages in real time, syncs history, stores everything in a local SQLite database, and exposes a REST API for sending messages and downloading media. It also supports exporting message history to human-readable text files via the `sync` command.

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

Authenticates with WhatsApp by displaying a QR code. Scan it with your phone (Settings > Linked Devices). If already logged in, shows account info. Required before running `core`, `sync`, `install-daemon`, or `install-cron`.

During first login, the CLI captures WhatsApp's initial history sync and stores it in `messages.db`. This is the only time WhatsApp pushes the full chat history.

The `--relogin` flag clears the existing session and message databases, re-displays the QR code for a fresh pairing, captures the initial history sync, and restarts the core daemon if it was previously running. Use this when the session is stale or when the initial history sync was missed.

### Core — WhatsApp Connection

```
whatsapp-cli core
```

Connects to WhatsApp, listens for messages, syncs history, and starts the REST API server. Requires login. Re-authentication may be required after ~20 days.

### Daemon Management

```
whatsapp-cli install-daemon
whatsapp-cli install-cron
whatsapp-cli start
whatsapp-cli stop
whatsapp-cli restart
```

| Command | Description |
|---------|-------------|
| `install-daemon` | Installs and starts the core daemon as a macOS LaunchAgent (`com.whatsapp-cli.core`). Runs on login and auto-restarts on crash. Requires login. Logs to `~/.local/share/whatsapp-cli/core.log`. |
| `install-cron` | Installs a cron job that runs `sync --catchup` every 5 minutes. Requires login. Logs to `~/.local/share/whatsapp-cli/sync.log`. |
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
| `info` | Shows data directory, chats folder (with file count), logged-in WhatsApp account, message database stats, daemon install status, and cron install status. |
| `reset` | Uninstalls daemons and cron, then wipes all local data including databases, synced text files, logs, and the WhatsApp session. |
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

The core daemon runs as `com.whatsapp-cli.core` via launchd. Logs go to `~/.local/share/whatsapp-cli/core.log`. The sync cron job logs to `~/.local/share/whatsapp-cli/sync.log`.

### Sync — Export Messages to Text Files

```
whatsapp-cli sync --catchup
whatsapp-cli sync --from=YYYY.MM.DD [--to=YYYY.MM.DD] [--output=DIR]
whatsapp-cli sync --from=YYYY.MM.DD:HH.MM [--to=YYYY.MM.DD:HH.MM] [--output=DIR]
whatsapp-cli sync --delete --from=YYYY.MM.DD [--to=YYYY.MM.DD] [--output=DIR]
```

Requires login. The WhatsApp connection does not need to be running. If the message database is empty (e.g. core has never run), sync will automatically connect to WhatsApp, wait for any history sync data, and store it before archiving. Data is stored in `~/.local/share/whatsapp-cli/`.

| Flag        | Required | Default      | Description                        |
|-------------|----------|--------------|------------------------------------|
| `--catchup` | *        |              | Catch up from the last synced message to now. Exits cleanly with 0 chats if no archive files exist yet. |
| `--delete`  |          |              | Delete synced messages in the `--from`/`--to` range. Requires `--from`. Empty files are removed afterwards. |
| `--from`    | *        |              | Start date/time (inclusive): `YYYY.MM.DD` or `YYYY.MM.DD:HH.MM` |
| `--to`      | No       | today        | End date/time: `YYYY.MM.DD` (inclusive through 23:59:59) or `YYYY.MM.DD:HH.MM` (exact) |
| `--output`  | No       | `~/.local/share/whatsapp-cli/chats`  | Output directory for text files    |

\* Either `--catchup`, `--delete`, or `--from` must be provided. `--delete` requires `--from`.

### Output

Sync always appends structured output to `~/.local/share/whatsapp-cli/sync.log`:

**Success:**

```
[2026-03-28 15:59:10]
RANGE: 2026-03-28 13:56:34 --> 2026-03-28 16:18:20
CHATS: 12
```

**Delete:**

```
[2026-03-28 15:59:10]
RANGE: 2026-03-01 00:00:00 --> 2026-03-15 23:59:59
DELETED
```

**Error:**

```
[2026-03-28 15:59:10]
ERROR:
no existing archive files found. Run with --from to create an initial archive first.
```

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

When WhatsApp pushes historical conversations (on first connect or periodically), the CLI processes each conversation:

1. Resolves the chat name (group name or contact name)
2. Extracts group participants directly from conversation metadata (the `Participant` field on the `Conversation` proto) and stores them in the `group_participants` table. This provides participant data even for groups the user has since left.
3. Extracts message content and media metadata. Non-text message types (stickers, contacts, locations, polls, reactions, etc.) are stored with descriptive placeholder text instead of being silently dropped.
4. Determines the message sender using multiple fallback fields: `Key.Participant`, `WebMessageInfo.Participant`, `PushName`, and finally the chat JID. WhatsApp populates these fields inconsistently, so checking all of them maximises sender attribution.
5. Stores each message with sender, timestamp, and media info

Push names (display names) and phone-to-LID mappings included in the history sync payload are processed automatically by whatsmeow and stored in the contacts database for later name resolution.

### WhatsApp API Limitations

WhatsApp's servers and the multidevice protocol have several known limitations that affect data completeness:

**Group names** — The `GetJoinedGroups` and `GetGroupInfo` APIs return participant lists for most groups but omit the group name (`Subject` field) for a significant fraction (~40%) of groups. This appears to be a server-side limitation that varies by group type, creation date, or privacy settings. When this happens, the CLI synthesizes a descriptive name from the group's resolved participant list, displayed in parentheses — e.g. `(Kevin, Eileen)` — to distinguish it from an actual group name.

**Group sender attribution** — History sync messages in group chats frequently omit the individual sender. The `Key.Participant` field (which should identify who sent the message) is often nil. A separate `WebMessageInfo.Participant` field and the `PushName` field sometimes carry this data, but many group messages arrive with no sender attribution at all. When the sender cannot be determined, the CLI displays `(group member)` instead.

**Groups the user has left** — `GetJoinedGroups` only returns currently joined groups. For groups the user has since left, the CLI falls back to individual `GetGroupInfo` calls, which may fail with 401/404 errors if the group no longer allows access.

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

## Sync Mode

### Execution Phases

Sync runs five phases in order on every invocation:

**Phase 0 — Refresh Group Data**
For any group chats missing participant data or with placeholder names (e.g. `Group 120363...`), connects to WhatsApp and resolves group metadata in three steps:
1. **Bulk fetch** — calls `GetJoinedGroups` to retrieve complete info (name + participants) for all currently joined groups in a single API call. This is more reliable than individual queries.
2. **Individual fallback** — for groups not returned by the bulk call (e.g. groups the user has left), makes individual `GetGroupInfo` calls.
3. **Name synthesis** — for groups where WhatsApp's API doesn't return a name (a known server-side limitation affecting ~40% of groups), generates a descriptive name from the first names of the group's participants, wrapped in parentheses to distinguish it from a real group name — e.g. `(Eileen, Kevin, TwensQueen)`.

Member lists are stored in the `group_participants` table; placeholder names in the `chats` table are upgraded to real names or, failing that, synthesized names in parentheses. Synthesized names are also upgradeable — if the API later returns a real name, it replaces the synthesized one.

**Phase 1 — Enhance Existing Archives**
Scans every existing `.txt` file in the output directory and:
- Resolves phone-number and group-placeholder chat titles to real names
- Expands group-as-participant lines to individual members
- Resolves unresolved participant identifiers (phone numbers, LIDs)
- Replaces group-name-as-sender in message lines with `(group member)` — WhatsApp's history sync often omits the individual sender for group messages, recording only the group JID
- Strips deprecated `SYNC RANGE` header lines from older archives

**Phase 2 — Cleanse Existing Files**
Removes message lines whose timestamps fall within the requested `[from, to]` window. Files left with zero messages are deleted. This ensures idempotency: re-running the same date range replaces stale data without duplicating messages outside the window.

**Phase 3 — Sync Messages**
For each chat with messages in the database within the requested window: queries new messages, reads retained messages (outside the window) from the existing file, merges them chronologically, and writes the file with an updated header. Group-JID senders are displayed as `(group member)` and group-JID participants are expanded to individual members.

**Phase 4 — Cleanup**
Deletes any `.txt` files with zero message lines.

When using `--delete`, only phases 2 and 4 run (cleanse + cleanup).

### Output Format

Each chat with messages in the requested range produces one `.txt` file named after the chat JID with `@` and `.` replaced by `_` (e.g. `353892379748_s_whatsapp_net.txt`). This makes filenames stable regardless of contact name changes. Chats with no messages do not produce files.

```
================================================================================
CHAT: Eileen Magan Dombrowski
CHAT JID: 353892379748@s.whatsapp.net
--------------------------------------------------------------------------------
PARTICIPANTS:
  - Eileen Magan Dombrowski; 353892379748; lid:137315053789298
  - Brian Dombrowski
================================================================================

[2026-03-02 11:57:29] Brian Dombrowski: Hey, how are you?
[2026-03-02 13:45:26] Eileen Magan Dombrowski: Good, thanks!
[2026-03-02 13:46:10] Eileen Magan Dombrowski: media
[2026-03-02 14:01:00] Eileen Magan Dombrowski: sticker
[2026-03-02 14:02:15] Brian Dombrowski: location: 53.3331, -6.2489
```

**Header fields:**
- **CHAT** — resolved contact name or group name. When the WhatsApp API doesn't return a group name, a synthesized name derived from participants is shown in parentheses, e.g. `(Kevin, Eileen)`.
- **CHAT JID** — WhatsApp JID (`number@s.whatsapp.net`, `number@lid`, `number@g.us`)
- **PARTICIPANTS** — each participant is listed as `{name}; {phone}; lid:{lid}`, with fields omitted when unavailable. Name is the resolved contact name, phone is the E.164 phone number, and LID is WhatsApp's internal linked ID. The `lid:` prefix distinguishes LIDs from phone numbers. The owner (Brian Dombrowski) is always listed by name only. When the history sync records a group itself as the message sender (rather than individuals), the group is expanded to its actual members using stored group membership data.

**Messages:** `[YYYY-MM-DD HH:MM:SS] Sender Name: content` — one per line, sorted chronologically.

Content types:
- Plain text and extended text are stored verbatim.
- Media (image, video, audio, document) displays `media` as content.
- Stickers, contacts, locations, group invites, polls, reactions, and other non-text message types are represented with descriptive text (e.g. `sticker`, `contact: Jane`, `location: 53.3331, -6.2489`, `poll`, `reaction: 👍`).
- View-once and ephemeral messages are unwrapped to their inner content.

In group chats, WhatsApp's history sync often omits the individual sender, recording only the group JID. When this happens, the sender is displayed as `(group member)` rather than the group name.

### Idempotency

Sync is designed to be run repeatedly. Running the same date range twice produces the same output. Overlapping ranges correctly merge: messages outside the new window are retained, messages inside are replaced with fresh data.

---

## Name Resolution

Contact names are resolved through a multi-step lookup, falling through until a non-phone-number name is found:

1. **messages.db `chats` table** — exact JID match
2. **messages.db `chats` table** — LIKE match on the phone number portion
3. **whatsapp.db `whatsmeow_contacts`** — lookup by full JID, `phone@s.whatsapp.net`, or `phone@lid`; returns `full_name` or `push_name`
4. **whatsapp.db LID mapping** — for LID-based senders, maps LID to phone via `whatsmeow_lid_map`, then re-looks up in `whatsmeow_contacts`
5. **Fallback** — raw sender string (phone number or JID)

At each step, results that look like phone numbers (all digits, optional leading `+`), group placeholder names (`Group 120363...`), or synthesized names (`(Kevin, Eileen)`) are skipped in favour of a more authoritative source. If the stored name is a placeholder or synthesized and a real name is resolved, the `chats` table is automatically updated. For LID-based senders where no contact name exists, the resolved phone number is returned instead of the opaque LID. See [WhatsApp API Limitations](#whatsapp-api-limitations) for cases where resolution cannot succeed.

### Participant Resolution

Each participant is resolved to all available identifiers (name, phone, LID) via `ResolveParticipant`:
- **LID-based JIDs** — maps LID to phone via `whatsmeow_lid_map`, then resolves name via contacts
- **Phone-based JIDs** — reverse-maps phone to LID via `whatsmeow_lid_map`, then resolves name
- Output format: `{name}; {phone}; lid:{lid}` with missing fields omitted

## Data Storage

All data is stored in `~/.local/share/whatsapp-cli/`.

| Path | Purpose |
|------|---------|
| `whatsapp.db` | whatsmeow session store (device credentials, contacts, LID mappings) |
| `messages.db` | Application message and chat database |
| `{chat_jid}/` | Downloaded media files, organized by chat |
| `chats/` | Sync output (text exports) |
| `core.log` | Core daemon log |
| `sync.log` | Sync operation log |

Both databases are opened by `NewMessageStore()`. The contacts DB is optional and non-fatal if missing. Both databases use WAL journal mode and a 5-second busy timeout to allow concurrent access from the core daemon and sync without locking conflicts.

### Database Schema

| Table | Key | Contents |
|-------|-----|----------|
| `chats` | `jid` (primary) | Chat JID, display name, last message timestamp |
| `messages` | `(id, chat_jid)` (composite) | Sender, content, timestamp, `is_from_me`, media metadata (type, filename, URL, encryption keys) |
| `group_participants` | `(group_jid, participant_jid)` (composite) | Maps each group chat to its individual member JIDs, fetched from WhatsApp via `GetGroupInfo` |

Tables are created on startup if they don't exist.

## Dependencies

- [whatsmeow](https://github.com/tulir/whatsmeow) — WhatsApp web multidevice API
- [go-sqlite3](https://github.com/mattn/go-sqlite3) — SQLite driver (requires CGO)
- [qrterminal](https://github.com/mdp/qrterminal) — QR code rendering in terminal
