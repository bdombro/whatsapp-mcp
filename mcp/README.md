# whatsapp-mcp

```
  .------------------.
  | WA > search()    |
  |   [MCP tools]    |
  `------------------'
```

Search chats and messages, find contacts, export history, and send or download media through a set of focused WhatsApp MCP tools.

The MCP queries a read-only message database and contacts the sync client only for sends and media downloads. Signal keys are detached before queries can run, and every write is audited and rate-limited.

> **Client compatibility:** MCP **2026-07-28 only**; legacy clients cannot connect.
> The workspace has been exercised with OpenCode v2.2.6 using explicit July
> configuration. Cursor's July support is deferred; Claude Code July runtime
> acceptance is not yet verified. Shipping a plugin manifest is not proof of
> client compatibility.

## Try It

> Find recent messages about the boiler appointment without sending anything.

## Features

- Search chats, contacts, message history, and context; export results or query safe SQL views.
- Send text, files, and audio or download media through the companion sync daemon.
- Keeps the WhatsApp session database inaccessible to SQL tools.
- Small footprint: a standalone ARM64 MCP binary; no Node/V8 runtime.

## Install

```bash
brew tap bdombro/whatsapp-mcp https://github.com/bdombro/whatsapp-mcp
brew install bdombro/whatsapp-mcp/whatsapp-client bdombro/whatsapp-mcp/whatsapp-mcp
brew services start whatsapp-client
brew services start whatsapp-mcp
```

Follow the [repository setup](../README.md#start-and-manage-the-services):
exactly one sync daemon, two services. The HTTP server binds `0.0.0.0:10008`
(OAuth metadata advertises `127.0.0.1`; pass `--host 127.0.0.1` for
loopback-only) and logs to `$(brew --prefix)/var/log/whatsapp-mcp.log`
(rotates to `.1` at 10 MiB). It uses OAuth and explicit browser approval; the
sync daemon must already be running. A remote agent is not automatically
authorized merely because WhatsApp is paired.


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


## Tools & CLI Commands

Every tool is exposed over Streamable HTTP (`whatsapp-mcp http [--host <addr>] [--port <n>] [--log-file <path>]`, default `0.0.0.0:10008`) and CLI (`whatsapp-mcp <tool> ['{"arg":"val"}']`).

| Tool | Scope | Kind | Description |
| --- | --- | --- | --- |
| `list_chats` | `whatsapp:read` | Read | List chats filtered by fuzzy name/participant query, ordered by most recent activity or name, with last message preview and pagination. |
| `get_chat` | `whatsapp:read` | Read | Metadata and latest message for a single chat `jid`. |
| `get_direct_chat_by_contact` | `whatsapp:read` | Read | Resolve a phone number to its direct chat (`@s.whatsapp.net` or linked `@lid`). |
| `get_contact_chats` | `whatsapp:read` | Read | All direct and group chats in which a contact `jid` participates. |
| `search_contacts` | `whatsapp:read` | Read | Search paired WhatsApp contacts by name, push name, business name, phone number, or LID. |
| `list_messages` | `whatsapp:read` | Read | Query messages by `chat_jid`, `sender_phone_or_lid`, `after`/`before` timestamps, or FTS5 BM25 keyword `query`, with optional surrounding context messages (`context_before`, `context_after`). |
| `get_message_context` | `whatsapp:read` | Read | Fetch chronological messages immediately before and after a target `message_id`. |
| `get_last_interaction` | `whatsapp:read` | Read | Most recent message exchanged with a contact `jid`. |
| `export` | `whatsapp:read` | Read | Bulk chronological export across one or all chats (`chat_jids`, `since`, `until`, `cursor`). Over MCP returns compact token-efficient text lines; on CLI/in-process returns structured JSON pages. |
| `sql` | `whatsapp:read` | Read | Execute one read-only `SELECT` or `WITH` query against `v_messages`, `v_chats`, `v_contacts`, and `messages_fts` (default 200 rows, max 5000, 10 s timeout). |
| `schema` | `whatsapp:read` | Read | Returns column definitions and query examples for `v_messages`, `v_chats`, `v_contacts`, and `messages_fts`. |
| `status` | `whatsapp:read` | Read | Returns server version (from `VERSION`), resolved data directory, and `daemon.sock` path. |
| `send_message` | `whatsapp:write` | Write | Send a text message (`recipient`, `message`, capped at `whatsapp_send_max_runes`, default 1000 chars) via `daemon.sock`. |
| `send_file` | `whatsapp:write` | Write | Send a local image, video, or document (`recipient`, `media_path`, optional `caption`). Path must be a regular file under `$HOME` or `/tmp` and never inside hidden folders (`~/.ssh`, `~/.config`) or `~/Library`. |
| `send_audio_message` | `whatsapp:write` | Write | Send an audio file as a WhatsApp voice note (`recipient`, `media_path`, same path containment rules). |
| `download_media` | `whatsapp:write` | Write | Download media for `(message_id, chat_jid)` via `daemon.sock`. Images, stickers, and audio/voice notes are returned directly as **native MCP `image` / `audio` content blocks** (when <= 5 MiB); larger files and documents return their saved path in `downloads/`. |

## Policy Configuration (`<data dir>/config.json`)

Optional JSON file read on startup:

```json
{
  "mcp": {
    "read_only": false,
    "disabled_tools": [],
    "mutating_tools_per_min": 10,
    "whatsapp_send_max_runes": 1000
  }
}
```

- Every tool call is recorded as a single JSON line in `<data dir>/mcp-audit.log`.
- Write tools (`send_message`, `send_file`, `send_audio_message`, `download_media`) enforce a sliding-window rate limit (`mutating_tools_per_min`, default 10/min).

## Environment Variables

Configure the service in `$(brew --prefix)/etc/whatsapp-mcp/env` (`chmod 600`, `KEY=VALUE`):

| Name | Required | Purpose |
| --- | --- | --- |
| `WHATSAPP_CLIENT_DATA_DIR` | Optional | Data directory (`messages.db`, `whatsapp.db`, `daemon.sock`); default `$(brew --prefix)/var/whatsapp-client` |
| `WHATSAPP_MCP_DATA_DIR` | Optional | Fallback data directory when `WHATSAPP_CLIENT_DATA_DIR` is unset |
| `HOMEBREW_PREFIX` | Optional | Homebrew prefix override for the default data directory (default `brew --prefix`) |
| `BUGSINK_DSN` | Optional | Error reporting DSN (unset or `""` disables reporting) |

## Troubleshooting

- **Client cannot connect:** confirm it supports July MCP, the service is
  running, and the client URL matches the configured port. OpenCode needs
  `"protocol": "2026-07-28"`; old `initialize` clients will fail.
- **Port occupied:** inspect the listener and stop only the service/process
  you own, or deliberately choose another port and update the client URL.
- **Works in a terminal, not as a service:** services don't inherit
  `~/.zshrc`. Put settings in `$(brew --prefix)/etc/whatsapp-mcp/env` and
  inspect `$(brew --prefix)/var/log/whatsapp-mcp.log` for errors.
- **Connect or pairing fails:** verify July and OAuth client support, then inspect the existing daemon status. Never fix this by starting a second daemon or deleting session databases.

## Development

See [docs/development.md](docs/development.md) for architecture, local builds,
testing and releases.
