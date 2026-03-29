# MCP Bridge

A pluggable [Model Context Protocol](https://modelcontextprotocol.io/) server that gives AI assistants access to personal data sources. Currently supports **WhatsApp** — search/read messages, search contacts, send messages — with the architecture ready for additional sources (Gmail, Google Drive, etc.).

Data is stored locally in SQLite and only sent to an LLM when accessed through MCP tools. Each data source registers its own namespaced tools (e.g. `whatsapp_list_chats`, `whatsapp_send_message`).

> *Caution:* as with many MCP servers, this is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). Project injection could lead to private data exfiltration.

## Architecture

A single Go binary (`whatsapp-cli`) with a pluggable `DataSource` interface. Each source owns its tools, storage, and lifecycle.

| Mode | Command | Description |
|------|---------|-------------|
| Core | `whatsapp-cli core` | WhatsApp connection daemon: stores messages in SQLite, exposes a REST API |
| MCP  | `whatsapp-cli mcp`  | MCP server over stdio. Loads all enabled sources and registers their tools |

Data flows: Claude/Cursor talks MCP (stdio) to `whatsapp-cli mcp`, which loads each data source. Read tools query local SQLite directly; write tools proxy through the source's backend (e.g. WhatsApp core daemon REST API).

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
