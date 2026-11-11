# whatsapp-mcp

```
  .------------------.
  | WA > search()    |
  |   [MCP tools]    |
  `------------------'
```

Search chats and messages, find contacts, export history, and send or download media through a set of focused WhatsApp MCP tools.

The MCP queries a read-only message database and contacts the sync client only for sends and media downloads. Signal keys are detached before queries can run, and every write is audited and rate-limited.

## Features

- Search chats, contacts, message history, and context; export results or query safe SQL views.
- Send text, files, and audio or download media through the companion sync daemon.
- Keeps the WhatsApp session database inaccessible to SQL tools.
- Small footprint: a standalone ARM64 MCP binary; no Node/V8 runtime.

## App Architecture

The stateless MCP server and JSON CLI are implemented in C++26 on `slimr-cpp`. It reads `messages.db` and uses the companion Go `whatsapp-client` only for sends and media downloads.

## Cryptographic Session-Key Isolation & SQLite Views

`whatsapp.db` (managed by `whatsmeow`) holds both contact names/LID mappings and sensitive Signal protocol session keys. Before running **any** query (including arbitrary user/agent queries in `sql`), `src/db.hpp` prepares an isolated read-only connection:

1. Opens `messages.db` with `SQLITE_OPEN_READONLY` and registers the deterministic `wa_utc(timestamp)` C++ scalar function (normalizing RFC3339, Go `time.String()`, and ISO timestamps to UTC `YYYY-MM-DD HH:MM:SS`).
2. `ATTACH`es `whatsapp.db` in `mode=ro`.
3. Copies only non-sensitive contact metadata (`t_names`, `t_contacts`, `t_lid_map`) into in-memory `TEMP` tables.
4. Immediately **`DETACH`es `whatsapp.db`** so Signal private keys, identity keys, and session ratchets are completely unreachable from the SQLite connection.
5. Creates `TEMP` views `v_messages`, `v_chats`, and `v_contacts` (resolving `@lid` and `@s.whatsapp.net` JIDs to human-readable contact and chat names).
6. Enables `PRAGMA query_only = ON`.

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

## HTTP Transport, OAuth 2.1 & Browser Connect UX

When launched via `whatsapp-mcp http --host 127.0.0.1 --port 8088` (default port `8088`), the server exposes `POST /mcp` protected by OAuth 2.1 + PKCE (`S256`):
- `GET /.well-known/oauth-protected-resource` (RFC 9728) & `GET /.well-known/oauth-authorization-server` (RFC 8414)
- `POST /register` (RFC 7591 dynamic client registration restricted to loopback redirect URIs)
- `GET /authorize` + `GET /authorize/qr` + `POST /authorize/approve` + `POST /authorize/cancel` (CSRF-protected browser Connect page; renders live QR SVG when `whatsapp-client` is unpaired, or shows the already-paired JID for explicit owner approval without touching `whatsapp.db`)
- `POST /token` (authorization code + PKCE `S256` + refresh token rotation with replay family revocation) and `POST /revoke` (revokes MCP access without disconnecting the WhatsApp session)

### CLI Examples

```bash
# Search messages across all chats using FTS5 BM25
./build/bin/whatsapp-mcp list_messages '{"query":"boiler","limit":10}'

# Bulk export messages since a local date
./build/bin/whatsapp-mcp export '{"since":"2026-11-01"}'

# Run a read-only SQL query against the isolated views
./build/bin/whatsapp-mcp sql '{"sql":"SELECT chat_name, COUNT(*) AS n FROM v_messages GROUP BY chat_jid ORDER BY n DESC LIMIT 10"}'

# Inspect the SQL view schema
./build/bin/whatsapp-mcp schema
```

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
[`../README.md`](../README.md#macos-build-prerequisites-vcpkg-setup) for both-component
setup. These MCP tests/builds do not access production data or start/pair the
daemon.

## Building & Testing

Build this directory with Clang/C++26 and libc++ FAST hardening, CMake 3.31+, Ninja, [just](https://just.systems), [vcpkg](https://vcpkg.io), and compatible clang-tidy. Set `VCPKG_ROOT` to a bootstrapped checkout and make `vcpkg` available on `PATH`; configure the local overlay explicitly:

```bash
export VCPKG_OVERLAY_PORTS="$VCPKG_ROOT/local-ports"
```

`just configure`, `test`, `build`, and `lint` automatically run `deps`, installing manifest dependencies under `build/deps/`. CMake uses installed `slimr::mcp` and `slimr::sqlite` targets with static SQLite/FTS5; manifest auto-install is disabled. Local overlay ports must already be registered; no source-checkout include overrides or global executable installs are used. Registry publication is separate.

Debug sanitizers and Release IPO builds have separate `build/cmake/Debug/` and `build/cmake/Release/` graphs. Both enable libc++ hardening and zero automatic-variable initialization; Debug adds ASan and fatal UBSan. Set `CXX` before the first configure to override the default Apple compiler on macOS or `clang++` on Linux; subsequent configures preserve CMake's cached compiler even when clang-tidy is added to `PATH`. CTest runs database and mock-socket fixtures with a finite timeout and isolated repo-local HOME/XDG/temporary directories. Release builds run those tests too.

```bash
just test     # Run C++26 unit & isolation tests (wa_utc, key isolation, FTS5, views, export, mock socket) with ASan + UBSan
just build    # Build/test Release binary at build/bin/whatsapp-mcp
just lint     # Analyze app and test units with the root .clang-tidy and compile_commands.json
just test-mcp # Inspector 2.14.0 initialize + tools/list only (requires compatible Node/npm)
just run --help # Run the existing Release binary without rebuilding
```

Inspector runs the server with an isolated HOME/XDG environment, no external CLI PATH or credentials, and Bugsink disabled; it never opens production databases, invokes tools, or starts/connects to the daemon. The repository-root Linux/macOS workflow also runs Go tests/build. CI requires the tested full `VCPKG_REF` commit variable; slimr packages use the pinned public registry without a dependency token. Linux execution and remote merge-gate enforcement are not claimed by local macOS verification.
