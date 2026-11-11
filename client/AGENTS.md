# AGENTS.md (`whatsapp/client`)

Written for: AI coding agents working in the `whatsapp/client` Go sync daemon.

`whatsapp-client` is the minimal Go `whatsmeow` background daemon that syncs WhatsApp into `~/.local/share/whatsapp-client/{whatsapp.db,messages.db}` and serves `/api/send` and `/api/download` on the owner-only `~/.local/share/whatsapp-client/daemon.sock` (`0600`) Unix socket.

## Commands

| Task | Command |
| --- | --- |
| Run Go tests | `just test` (`go test ./...`) |
| Build `dist/whatsapp-client` | `just build` |
| Set up the LaunchAgent | `../scripts/cutover.sh` (uses the workspace binary; never copy it to `~/.local/bin`) |

## Rules

1. **Unix socket only (`0600`):** Never expose `/api/send` or `/api/download` on a TCP loopback port (which web pages in a browser could reach). Always bind `daemon.sock` inside the `0700` data directory with `0600` permissions.
2. **2FA OTP Redaction (`core/twofa.go`):** Every incoming message must pass through OTP redaction before being stored in `messages.db`.
3. **PII-free Bugsink reports (`core/bugsink.go`):** Never include message text, phone numbers, JIDs, or contact names in Sentry/Bugsink events.
4. **Single-session rule:** Never run `whatsapp-client daemon` while another daemon holds `whatsapp.db`.
