# WhatsApp MCP Server

> This app was originally forked from https://github.com/lharries/whatsapp-mcp.git to merge outstanding bugs PRs and to add a feature for text archiving. The text archiving feature is useful for chatbots to index on.

A [Model Context Protocol](https://modelcontextprotocol.io/) server for WhatsApp. Search and read your personal WhatsApp messages (including media), search contacts, and send messages to individuals or groups — all through an AI assistant like Claude or Cursor.

It connects to your **personal WhatsApp account** via the WhatsApp web multidevice API (using [whatsmeow](https://github.com/tulir/whatsmeow)). Messages are stored locally in SQLite and only sent to an LLM when accessed through MCP tools.

> *Caution:* as with many MCP servers, the WhatsApp MCP is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). This means that project injection could lead to private data exfiltration.

## Architecture

A single Go binary (`whatsapp-cli`) handles everything: WhatsApp connection, message storage, REST API, and the MCP server. No Python runtime required.

| Mode | Command | Description |
|------|---------|-------------|
| Core | `whatsapp-cli core` | Connects to WhatsApp, stores messages in SQLite, exposes a REST API for sending/downloading |
| MCP  | `whatsapp-cli mcp`  | MCP server over stdio for AI assistants. Reads from SQLite directly, sends via the core daemon's REST API |

Data flows: Claude/Cursor talks MCP (stdio) to `whatsapp-cli mcp`, which reads from SQLite for queries and calls the core daemon's HTTP API for sends and media downloads. The core daemon maintains the WhatsApp connection and keeps the database current.

See the [product spec](docs/spec.md) for full details.

## Prerequisites

- Go (to build)
- FFmpeg (*optional*) — only needed for automatic audio format conversion when sending voice messages

## Quick Start

1. **Clone the repository**

   ```bash
   git clone https://github.com/lharries/whatsapp-mcp.git
   cd whatsapp-mcp
   ```

2. **Install**

   ```bash
   chmod +x ./tasks.sh
   ./tasks.sh install
   ```

   This builds the Go binary, copies it to `/usr/local/bin/whatsapp-cli`, sets up the core daemon, and adds shell completions.

3. **Log in** (first time only)

   ```bash
   whatsapp-cli login
   ```

   Scan the QR code with your WhatsApp app. The initial history sync will be captured during login. The daemon will handle reconnection from now on.

4. **Configure the MCP server** in your AI client:

   ```json
   {
     "mcpServers": {
       "whatsapp": {
         "command": "whatsapp-cli",
         "args": ["mcp"]
       }
     }
   }
   ```

   - **Claude Desktop**: `~/Library/Application Support/Claude/claude_desktop_config.json`
   - **Cursor**: `~/.cursor/mcp.json`

5. **Restart Claude Desktop / Cursor**

## Managing the Daemon

Install the core daemon (runs on login, auto-restarts) and manage it:

```bash
whatsapp-cli install-daemon
whatsapp-cli start
whatsapp-cli stop
whatsapp-cli restart
```

Other commands:

```bash
# Show status and data locations
whatsapp-cli info

# Uninstall daemon and wipe all data
whatsapp-cli reset

# Full uninstall (reset + remove binaries)
whatsapp-cli uninstall
```

## Troubleshooting

- **QR code not displaying**: Restart the CLI. Check that your terminal supports QR rendering.
- **Already logged in**: The CLI reconnects automatically without a QR code.
- **Device limit reached**: Remove an existing device from WhatsApp on your phone (Settings > Linked Devices).
- **No messages loading**: Initial history sync can take several minutes for large accounts. History is only pushed during first pairing. If your database is empty, run `whatsapp-cli login --relogin` to re-pair and capture the initial sync.
- **Out of sync**: Run `whatsapp-cli reset` to wipe all data, then `whatsapp-cli login` to re-authenticate.
- **Session expired / 405 error**: Run `whatsapp-cli login --relogin` to clear the stale session and re-pair. The daemon will be restarted automatically.
For additional Claude Desktop troubleshooting, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues).

### Windows

`go-sqlite3` requires CGO, which is disabled by default on Windows. Install a C compiler via [MSYS2](https://www.msys2.org/) (add `ucrt64\bin` to PATH), then:

```bash
cd whatsapp-cli
go env -w CGO_ENABLED=1
go build -o whatsapp-cli .
./whatsapp-cli --core
```
