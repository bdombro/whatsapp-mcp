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

## Getting Started

Install alongside the MCP using the [repository instructions](../README.md#installation).
Choose exactly one daemon manager: the existing LaunchAgent or Homebrew,
never both. Check `whatsapp-client status` before starting or changing a daemon.
Keep the existing data directory; installation is not a reset or migration.

For a fresh unpaired setup, the running daemon exposes QR state to the MCP's
browser Connect page. Prefer that flow to a second terminal login process.
The client itself is not an MCP server.


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

## Troubleshooting

- **Session/socket already in use:** inspect the existing daemon; never start
  another process against the same session.
- **Not paired:** use the MCP browser Connect page with the existing daemon.
  Stop the daemon before a deliberate standalone `login`; `--relogin` wipes
  the session and is not a routine recovery step.
- **No new messages:** inspect daemon status and logs before changing files.
  Do not delete either database to repair a connection.

Implementation and socket details are in the [developer reference](docs/development.md).


## Development

### macOS prerequisites

Install [Homebrew](https://brew.sh) and Xcode Command Line Tools
(`xcode-select --install`; finish installation before continuing). From `client/`:

```bash
brew install go just
go version
BUGSINK_DSN="" just test
just build
```

Go must satisfy [`go.mod`](./go.mod). Go resolves module dependencies during
tests/builds; this client does not use vcpkg or require system SQLite.
For the C++ companion's vcpkg, compiler and native dependencies, follow the
[repository setup](../README.md#development).
Building and testing do not start the daemon or pair a WhatsApp session.

```bash
just test       # go test ./...
just build      # Build dist/whatsapp-client
```
