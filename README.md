# WhatsApp MCP Server

> This app was originally forked from https://github.com/lharries/whatsapp-mcp.git to merge outstanding bugs PRs and to add a feature for text archiving. The text archiving feature is useful for chatbots to index on.

A [Model Context Protocol](https://modelcontextprotocol.io/) server for WhatsApp. Search and read your personal WhatsApp messages (including media), search contacts, and send messages to individuals or groups — all through an AI assistant like Claude or Cursor.

It connects to your **personal WhatsApp account** via the WhatsApp web multidevice API (using [whatsmeow](https://github.com/tulir/whatsmeow)). Messages are stored locally in SQLite and only sent to an LLM when accessed through MCP tools.

![WhatsApp MCP](./example-use.png)

> To get updates on this and other projects I work on [enter your email here](https://docs.google.com/forms/d/1rTF9wMBTN0vPfzWuQa2BjfGKdKIpTbyeKxhPMcEzgyI/preview)

> *Caution:* as with many MCP servers, the WhatsApp MCP is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). This means that project injection could lead to private data exfiltration.

## Architecture

| Component | Directory | Language | Description |
|-----------|-----------|----------|-------------|
| WhatsApp CLI | `whatsapp-cli/` | Go | Connects to WhatsApp, stores messages in SQLite, exposes a REST API for sending/downloading. See [spec](docs/whatsapp-cli-spec.md). |
| MCP Server | `whatsapp-mcp-server/` | Python | MCP tool interface for AI assistants. Reads from SQLite directly, sends via the CLI's HTTP API. See [spec](docs/whatsapp-mcp-server-spec.md). |

Data flows: Claude/Cursor talks MCP (stdio) to the Python server, which reads from SQLite for queries and calls the Go CLI's HTTP API for sends and media downloads. The CLI maintains the WhatsApp connection and keeps the database current.

## Prerequisites

- Go
- Python 3.11+
- [UV](https://astral.sh/uv) (Python package manager): `curl -LsSf https://astral.sh/uv/install.sh | sh`
- Claude Desktop or Cursor
- FFmpeg (*optional*) — only needed for automatic audio format conversion when sending voice messages

## Quick Start

1. **Clone the repository**

   ```bash
   git clone https://github.com/lharries/whatsapp-mcp.git
   cd whatsapp-mcp
   ```

2. **Install**

   ```bash
   just install
   ```

   This builds the Go binary, copies it to `/usr/local/bin/whatsapp-cli`, installs the MCP server to `/usr/local/lib/whatsapp-mcp-server/`, sets up the core daemon, and adds shell completions.

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
         "command": "whatsapp-mcp-server"
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
- **UV permission issues**: Ensure `uv` is on your PATH or use the full path in the MCP config.

For additional Claude Desktop troubleshooting, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues).

### Windows

`go-sqlite3` requires CGO, which is disabled by default on Windows. Install a C compiler via [MSYS2](https://www.msys2.org/) (add `ucrt64\bin` to PATH), then:

```bash
cd whatsapp-cli
go env -w CGO_ENABLED=1
go build -o whatsapp-cli .
./whatsapp-cli --core
```
