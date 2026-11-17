# whatsapp-mcp

```
   .----------------.
  /  chats + tools   \
 |    <>       <>     |
  \_______  ________/
          \/
```

Search and review WhatsApp chats, export message history, and send messages or media through one MCP. A separate sync client keeps the WhatsApp session connected and the local message database current.

The split keeps message access tightly scoped: the sync client owns the WhatsApp session, while the MCP queries the message database through isolated, read-only views. Signal keys are detached before queries run, and message bodies are treated as untrusted content.

> **Client compatibility:** MCP **2026-07-28 only**; legacy clients cannot connect.
> The workspace has been exercised with OpenCode v2.2.6 using explicit July
> configuration. Cursor's July support is deferred; Claude Code July runtime
> acceptance is not yet verified. Shipping a plugin manifest is not proof of
> client compatibility.

## Try It

After setup, ask your agent:

> Find the recent messages about the boiler appointment, without sending anything.

## Features

- Search contacts and chats, retrieve message context, run read-only SQL, and export history.
- Send text, files, and voice notes or download media through the local sync client.
- Signal session keys stay isolated from the MCP query connection; write calls are rate-limited and audited.
- Small footprint: only the focused sync daemon stays resident; the MCP is a separate standalone binary.

> **Security & untrusted content:** Like any tool that reads private messages and can send messages, message bodies are treated strictly as untrusted data. One-time passcodes are scrubbed before hitting disk, `whatsapp.db` Signal cryptographic keys are detached before any SQL query executes, file sends are restricted to safe user paths, every mutating tool call is logged to `mcp-audit.log` and rate-limited, and `config.json` can disable write tools entirely (`"read_only": true`).

## Install

```bash
brew tap bdombro/whatsapp-mcp https://github.com/bdombro/whatsapp-mcp
brew install bdombro/whatsapp-mcp/whatsapp-client bdombro/whatsapp-mcp/whatsapp-mcp
```

Two formulas, two services: `whatsapp-client` (Go sync daemon) and
`whatsapp-mcp` (C++26 MCP server). Check
[releases](https://github.com/bdombro/whatsapp-mcp/releases) for published
binaries first; if none is published yet, build from source per
[development](docs/development.md). Installing a Cursor/Claude manifest only
configures an endpoint; it doesn't install or start either service.

## Start and Manage the Services

**Run exactly one sync daemon.** Check `whatsapp-client status` first, and
never run the Homebrew `whatsapp-client` service alongside the older
`com.bdombro.whatsapp-client` LaunchAgent (`whatsapp-client start`).

```bash
brew services start whatsapp-client
brew services start whatsapp-mcp
brew services info whatsapp-client
brew services info whatsapp-mcp
```

Restart or stop with `brew services restart|stop <formula>`. The MCP HTTP
server binds `127.0.0.1:10008` (`http://127.0.0.1:10008/mcp`; pass
`--host 0.0.0.0` to expose on all interfaces, with OAuth metadata still
advertising `127.0.0.1`).
Logs go to `$(brew --prefix)/var/log/whatsapp-client.log` and
`$(brew --prefix)/var/log/whatsapp-mcp.log` (each rotates to `.1` at 10 MiB).
To run the MCP in the foreground instead, use `whatsapp-mcp http`; don't run
the foreground server and service on the same port.

## Pair WhatsApp

Connect with a client supporting both July MCP and the required OAuth flow.
The browser Connect page shows a WhatsApp QR code when unpaired and asks for
explicit approval when already paired. Scan through WhatsApp's Linked Devices
screen. Pairing alone does not grant MCP access. This client/OAuth combination
still needs runtime acceptance; packaged manifests aren't evidence it works
end to end.


## Connect Your Agent

After starting the service, add this server to OpenCode v2.2.6's
`opencode.jsonc` (merge it with any existing servers):

```json
{
  "mcp": {
    "servers": {
      "whatsapp-mcp": {
        "type": "remote",
        "url": "http://127.0.0.1:10008/mcp",
        "protocol": "2026-07-28"
      }
    }
  }
}
```

Use either an explicit server entry or a plugin manifest, not both.
Alternative client schemas are in the
[developer reference](docs/development.md). The compatibility notice above
applies to every transport.


## Environment Variables

Both services read optional `KEY=VALUE` lines from
`$(brew --prefix)/etc/<formula>/env` (`chmod 600`):

| Name | Required | Purpose |
| --- | --- | --- |
| `WHATSAPP_CLIENT_DATA_DIR` | Optional | Data directory for both components (default `$(brew --prefix)/var/whatsapp-client`) |
| `WHATSAPP_MCP_DATA_DIR` | Optional | Fallback data directory when `WHATSAPP_CLIENT_DATA_DIR` is unset |
| `HOMEBREW_PREFIX` | Optional | Homebrew prefix override for default data and log paths (default `brew --prefix`) |
| `BUGSINK_DSN` | Optional | Error reporting DSN (unset or `""` disables reporting) |

Set the same data directory for both services; the MCP reads the client's
databases and socket.

## Runtime Data

| Path | Mode | Purpose |
| --- | --- | --- |
| `$(brew --prefix)/var/whatsapp-client/` | `0700` | Owner-only data directory |
| `…/whatsapp.db` | `0600` | `whatsmeow` device session, Signal keys, contact names (`whatsmeow_contacts`), and LID mappings (`whatsmeow_lid_map`) |
| `…/messages.db` | `0600` | Synced chats, messages, group participants, and `messages_fts` FTS5 index (WAL mode) |
| `…/daemon.sock` | `0600` | Unix domain socket for `/api/send` and `/api/download` |
| `…/mcp-audit.log` | `0600` | Append-only audit log of every MCP/CLI tool invocation |
| `…/config.json` | `0600` | Optional MCP policy overrides (`read_only`, `disabled_tools`, `mutating_tools_per_min`, `whatsapp_send_max_runes`) |
| `…/downloads/` | `0700` | Media files downloaded by `download_media` |

## Troubleshooting

- **Client cannot connect:** confirm it supports July MCP, the service is
  running, and the client URL matches the configured port. OpenCode needs
  `"protocol": "2026-07-28"`; old `initialize` clients will fail.
- **Port occupied:** inspect the listener and stop only the service/process
  you own, or deliberately choose another port and update the client URL.
- **Works in a terminal, not as a service:** services don't inherit
  `~/.zshrc`. Put settings in `$(brew --prefix)/etc/<formula>/env` and inspect
  `$(brew --prefix)/var/log/` for errors.
- **Connect or pairing fails:** verify July and OAuth client support, then inspect the existing daemon status. Never fix this by starting a second daemon or deleting session databases.

## Development

See [docs/development.md](docs/development.md) for architecture, local builds,
testing and releases.
