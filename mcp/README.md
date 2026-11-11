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

## Installation and Pairing

Follow the [repository installation and single-daemon setup](../README.md#installation).
Both components retain the existing `~/.local/share/whatsapp-client/` data.
The HTTP server uses OAuth and explicit browser approval; the pairing daemon
must already be running. A remote agent is not automatically authorized merely
because WhatsApp is paired.


## Connect Your Agent

After starting the service, add this server to OpenCode v2.2.6's
`opencode.jsonc` (merge it with any existing servers):

```json
{
  "mcp": {
    "servers": {
      "whatsapp-mcp": {
        "type": "remote",
        "url": "http://127.0.0.1:8088/mcp",
        "protocol": "2026-07-28"
      }
    }
  }
}
```

Use either an explicit server entry or a plugin manifest, not both.
Alternative client schemas and stdio configuration are in the
[developer reference](docs/development.md). The compatibility notice above
applies to every transport.


## Tools & CLI Commands

Every tool is exposed over Streamable HTTP (`whatsapp-mcp http --host 127.0.0.1 --port 8088`), stdio MCP (`whatsapp-mcp mcp`, protocol `2026-07-28`), and CLI (`whatsapp-mcp <tool> ['{"arg":"val"}']`). The companion Go `whatsapp-client` identity and data paths are unchanged.

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
| `status` | `whatsapp:read` | Read | Returns server version (`2.0.0`), resolved data directory, and `daemon.sock` path. |
| `send_message` | `whatsapp:write` | Write | Send a text message (`recipient`, `message`, capped at `whatsapp_send_max_runes`, default 1000 chars) via `daemon.sock`. |
| `send_file` | `whatsapp:write` | Write | Send a local image, video, or document (`recipient`, `media_path`, optional `caption`). Path must be a regular file under `$HOME` or `/tmp` and never inside hidden folders (`~/.ssh`, `~/.config`) or `~/Library`. |
| `send_audio_message` | `whatsapp:write` | Write | Send an audio file as a WhatsApp voice note (`recipient`, `media_path`, same path containment rules). |
| `download_media` | `whatsapp:write` | Write | Download media for `(message_id, chat_jid)` via `daemon.sock`. Images, stickers, and audio/voice notes are returned directly as **native MCP `image` / `audio` content blocks** (when <= 5 MiB); larger files and documents return their saved path in `downloads/`. |

## Policy Configuration (`~/.local/share/whatsapp-client/config.json`)

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

- Every tool call is recorded as a single JSON line in `~/.local/share/whatsapp-client/mcp-audit.log`.
- Write tools (`send_message`, `send_file`, `send_audio_message`, `download_media`) enforce a sliding-window rate limit (`mutating_tools_per_min`, default 10/min).

## Troubleshooting

- **Client cannot connect:** confirm it supports July MCP, the service is
  running, and the client URL matches the configured port. OpenCode needs
  `"protocol": "2026-07-28"`; old `initialize` clients will fail.
- **Port occupied:** inspect the listener and stop only the service/process
  you own, or deliberately choose another port and update the client URL.
- **Works in a terminal, not as a service:** launchd does not inherit
  `~/.zshrc`. Configure the service environment separately or use the
  plugin's supported private configuration file; inspect
  `/opt/homebrew/var/log/` for errors.
- **Connect or pairing fails:** verify July and OAuth client support, then inspect the existing daemon status. Never fix this by starting a second daemon or deleting session databases.
- **Source build cannot find vcpkg or slimr ports:** load the development
  environment, bootstrap vcpkg, and register both slimr links and the pinned
  libhv overlay. Never repair this by editing installed headers.
- **Compiler/hardening check fails:** upgrade the Xcode toolchain; set
  `CXX` before first configure. CMake caches the compiler, so an intentional
  compiler change needs a fresh configuration tree.


## Development

This section is for building or changing the server, not configuring an
installed binary. [Architecture, transport reference and test details](docs/development.md)
are separate from the user setup flow.

<details>
<summary>macOS source-build setup (vcpkg and local slimr overlays)</summary>

## macOS Build Prerequisites (vcpkg setup)

Install [Homebrew](https://brew.sh) and current Xcode Command Line Tools
(`xcode-select --install`; finish installation before continuing), or full Xcode.
Apple Clang 21 is the verified C++26/libc++ toolchain; older installations need
upgrading. These instructions assume Apple Silicon Homebrew at `/opt/homebrew`.

Add this environment snippet to `~/.zshrc`, then open a new terminal (or run
`source ~/.zshrc`). Use `~/.zshrc`, not `~/.zshenv`, to avoid changing
non-interactive zsh scripts:

`local-ports` is the dedicated vcpkg overlay directory for local development
linking: `just publish --local --all` registers symlinks to your slimr-cpp checkout,
overriding published packages without making a registry release. See the
[publishing guide on slimr-cpp](https://github.com/bdombro/slimr-cpp/blob/main/docs/publishing.md#local-environment).

```bash
export VCPKG_ROOT="$HOME/.local/share/vcpkg"
export VCPKG_OVERLAY_PORTS="$VCPKG_ROOT/local-ports"
# Use your existing workspace checkout instead when available.
export SLIMR_CPP="${SLIMR_CPP:-$HOME/dev/bdombro/slimr-cpp}"
export CXX="/usr/bin/clang++"
export CLANG_TIDY="/opt/homebrew/opt/llvm/bin/clang-tidy"
export PATH="$VCPKG_ROOT:/opt/homebrew/opt/ruby/bin:/opt/homebrew/bin:$PATH"
```

Then install and build from this `mcp/` directory:

```bash
brew install cmake ninja just llvm node ruby autoconf automake libtool pkgconf
mkdir -p "$(dirname "$VCPKG_ROOT")"
if [ ! -d "$VCPKG_ROOT/.git" ]; then
  git clone https://github.com/microsoft/vcpkg.git "$VCPKG_ROOT"
fi
"$VCPKG_ROOT/bootstrap-vcpkg.sh" -disableMetrics
mkdir -p "$(dirname "$SLIMR_CPP")"
if [ ! -d "$SLIMR_CPP/.git" ]; then
  git clone https://github.com/bdombro/slimr-cpp.git "$SLIMR_CPP"
fi
(cd "$SLIMR_CPP" && just publish --local --all)
ln -s "$SLIMR_CPP/ports/libhv" "$VCPKG_OVERLAY_PORTS/libhv"

"$CXX" --version
vcpkg version
BUGSINK_DSN="" just test
just build
```

The libhv link is a one-time step: if it exists, verify that it points to the
chosen checkout's `ports/libhv` before skipping the command. Never overwrite
a conflicting port directory or modify installed headers/caches. Current HTTP
builds need both local slimr overlays and the separately pinned libhv port;
the older published registry baseline alone is not an HTTP build substitute.
`just deps` installs dependencies into this directory's `build/deps/`, including
static SQLite/FTS5, curl and OpenSSL; separate Homebrew library installs are
unnecessary.

Bootstrapping puts the executable at `$VCPKG_ROOT/vcpkg`, with no `bin/`
subdirectory. Keg-only Homebrew Ruby supplies `/opt/homebrew/opt/ruby/bin/ruby`.
Homebrew LLVM supplies lint tooling; select a different compatible `CXX`
before first configure if needed, since CMake caches it. For reproducible
vcpkg tooling, check out the tested full `VCPKG_REF` commit before bootstrapping;
package registry baselines pin resolution separately.
Go is only needed to build the companion daemon; see
[`../README.md`](../README.md#development) for both-component
setup. These MCP tests/builds do not access production data or start/pair the
daemon.

</details>

After the source build succeeds, stage and install the local Homebrew formula:

```bash
(cd .. && just install-local whatsapp-mcp)
```

Installing a formula and starting its service are separate actions.
For a source-only foreground run, use
`./build/bin/whatsapp-mcp http` from this mcp/ directory.
