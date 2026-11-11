# whatsapp-client

```
   .------------.
  |  SYNC  <->  |
  |  chats ...  |
   `-----\______/
          \___/
```

Keep your WhatsApp session connected and your local message history up to date. The daemon syncs chats and messages continuously, and provides a private local socket for the companion MCP to send messages and download media.

The client owns the paired session and sync database; the companion MCP handles queries and message access separately.

## Features

1. **WhatsApp Web Multi-Device Sync:**
   - Connects using the paired device session in `~/.local/share/whatsapp-client/whatsapp.db`.
   - Writes chats, messages, media metadata (`url`, `direct_path`, `media_key`, `file_sha256`, `file_enc_sha256`, `file_length`), and group participants into `messages.db` (WAL mode, FTS5 `messages_fts` triggers).
   - Automatically scrubs messages matching one-time verification code (OTP) patterns before writing them to disk.
2. **Owner-Only Unix Socket API (`daemon.sock`, `0600`):**
   - `POST /api/send` — sends text messages, media/documents, or voice notes (`audio/ogg; codecs=opus`) to a recipient phone number or JID.
   - `POST /api/download` — downloads and decrypts media for a stored `(message_id, chat_jid)` into `~/.local/share/whatsapp-client/downloads/`, returning the local file path and MIME type.
3. **Error Reporting:**
   - Optionally reports unexpected disconnects, logged-out session events, and SQLite write faults via `sentry-go` with message text, phone numbers, and contact names excluded or scrubbed. Set `BUGSINK_DSN` to enable; unset or empty disables reporting.
4. **Small footprint:** A focused background sync daemon, separate from the MCP process and its query workload.

## CLI Commands

| Command | Description |
| --- | --- |
| `whatsapp-client daemon` | Run the sync daemon in the foreground (invoked by `launchd` via `com.bdombro.whatsapp-client.plist`) |
| `whatsapp-client login [--relogin]` | Pair device by scanning a terminal QR code (`--relogin` wipes existing session first; stop the daemon before running) |
| `whatsapp-client start` | Bootstrap `~/Library/LaunchAgents/com.bdombro.whatsapp-client.plist` |
| `whatsapp-client stop` | Bootout the LaunchAgent and wait for `daemon.sock` to close |
| `whatsapp-client restart` | Restart the LaunchAgent |
| `whatsapp-client status [--json]` | Report daemon PID/socket status, WhatsApp connection state, and total chat/message counts |
| `whatsapp-client reset --yes` | Delete `whatsapp.db` and `messages.db` (requires daemon to be stopped) |

## Environment Variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `WHATSAPP_CLIENT_DATA_DIR` | `~/.local/share/whatsapp-client` | Directory for `whatsapp.db`, `messages.db`, `daemon.sock`, and `daemon.log` |
| `BUGSINK_DSN` | Unset (disabled) | Sentry DSN (`""` disables) |

## App Architecture

Implemented in Go with [`whatsmeow`](https://github.com/tulir/whatsmeow). Go module dependencies are resolved by `go build` and `go test`; there is no separate package installation step. Its sibling MCP is a standalone C++26 binary.

## Development

```bash
just test       # go vet ./... && go test ./...
just build      # Build dist/whatsapp-client
```
