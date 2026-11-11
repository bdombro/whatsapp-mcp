# WhatsApp Client: Developer Reference

[User setup](../README.md)

## Features

1. **WhatsApp Web Multi-Device Sync:**
   - Connects using the paired device session in `~/.local/share/whatsapp-client/whatsapp.db`.
   - Writes chats, messages, media metadata (`url`, `direct_path`, `media_key`, `file_sha256`, `file_enc_sha256`, `file_length`), and group participants into `messages.db` (WAL mode, FTS5 `messages_fts` triggers).
   - Automatically scrubs messages matching one-time verification code (OTP) patterns before writing them to disk.
2. **Owner-Only Unix Socket API (`daemon.sock`, `0600`):**
   - `GET /api/pair/status` — returns `{ paired, connected, jid }` (minimal masked identity; never Signal keys).
   - `GET /api/pair/qr` — returns expiring QR state (`available` / `waiting` / `timeout` / `already_paired`) when unpaired without logging QR payloads or wiping an already-paired `whatsapp.db`.
   - `POST /api/pair/cancel` — cancels an active unpaired QR handshake (`409` if already paired).
   - `POST /api/send` — sends text messages, media/documents, or voice notes (`audio/ogg; codecs=opus`) to a recipient phone number or JID.
   - `POST /api/download` — downloads and decrypts media for a stored `(message_id, chat_jid)` into `~/.local/share/whatsapp-client/downloads/`, returning the local file path and MIME type.
3. **Error Reporting:**
   - Optionally reports unexpected disconnects, logged-out session events, and SQLite write faults via `sentry-go` with message text, phone numbers, and contact names excluded or scrubbed. Set `BUGSINK_DSN` to enable; unset or empty disables reporting.
4. **Small footprint:** A focused background sync daemon, separate from the MCP process and its query workload.

## App Architecture

Implemented in Go with [`whatsmeow`](https://github.com/tulir/whatsmeow). Go module dependencies are resolved by `go build` and `go test`; there is no separate package installation step. Its sibling MCP is a standalone C++26 binary.
