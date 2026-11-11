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

## Features

- Search contacts and chats, retrieve message context, run read-only SQL, and export history.
- Send text, files, and voice notes or download media through the local sync client.
- Signal session keys stay isolated from the MCP query connection; write calls are rate-limited and audited.
- Small footprint: only the focused sync daemon stays resident; the MCP is a separate standalone binary.

> **Security & untrusted content:** Like any tool that reads private messages and can send messages, message bodies are treated strictly as untrusted data. One-time passcodes are scrubbed before hitting disk, `whatsapp.db` Signal cryptographic keys are detached before any SQL query executes, file sends are restricted to safe user paths, every mutating tool call is logged to `mcp-audit.log` and rate-limited, and `config.json` can disable write tools entirely (`"read_only": true`).

## `.mcp.json` Configuration

`whatsapp-mcp/mcp` runs standalone over Streamable HTTP (`http://127.0.0.1:8088/mcp`, with OAuth 2.1 + PKCE and a browser QR/approval UI), stdio (`whatsapp-mcp mcp`, protocol `2026-07-28`), or direct CLI (`whatsapp-mcp <tool> ['{"arg":"val"}']`), exposing canonical unprefixed tool names (`list_chats`, `get_chat`, `list_messages`, `export`, `sql`, `schema`, `send_message`, `status`, …).

To run `whatsapp-mcp` as a standalone MCP server in `.mcp.json`:

```json
{
  "mcpServers": {
    "whatsapp-mcp": {
      "command": "/absolute/path/to/whatsapp-mcp/mcp/build/bin/whatsapp-mcp",
      "args": ["mcp"],
      "env": {
        "WHATSAPP_CLIENT_DATA_DIR": "/absolute/path/to/whatsapp-client-data"
      }
    }
  }
}
```

## App Architecture

The `client/` is a Go daemon using `whatsmeow`; the stateless `mcp/` server and JSON CLI are implemented in C++26 on `slimr-cpp`.

```text
plugins/whatsapp-mcp/
├── justfile                  # Runs `just test` and `just build` across both `client/` and `mcp/`
├── README.md                 # Architecture overview, data directory layout & cutover guide
├── AGENTS.md                 # Repository-wide safety & schema invariants for AI agents
├── scripts/
│   └── cutover.sh            # Safe single-daemon migration script (`whatsapp-mcp` -> `whatsapp-client`)
├── client/                   # Go 1.26 `whatsmeow` sync daemon (`whatsapp-client`)
│   ├── justfile              # `just test`, `just build`
│   ├── README.md             # Daemon CLI & Unix socket documentation
│   ├── AGENTS.md             # Go sync daemon rules
│   ├── main.go, cmd.go       # Entrypoint & daemon/login/start/stop/status/reset CLI
│   ├── daemon.go, system.go  # LaunchAgent (`com.bdombro.whatsapp-client.plist`) management
│   ├── core/                 # 2FA OTP redaction (`twofa.go`), log trimming, Bugsink (`sentry-go`)
│   └── whatsapp/             # `whatsmeow` sync, `messages.db` store, `daemon.sock` (`/api/send`, `/api/download`)
└── mcp/                      # C++26 `slimr-cpp` MCP server & narrow JSON CLI (`whatsapp-mcp`)
    ├── vcpkg.json            # Package dependencies installed under build/deps/
    ├── CMakeLists.txt        # App/test targets, imported dependencies, safety settings
    ├── justfile              # CMake/Ninja configure, test, build, lint, test-mcp
    ├── README.md             # 15 MCP tools, SQLite view isolation & policy config reference
    ├── AGENTS.md             # C++26 MCP conventions & cryptographic key isolation rules
    ├── src/
    │   ├── schemas.hpp       # Reflected input structs for all 15 tools
    │   ├── time_util.hpp     # Deterministic `wa_utc` timestamp normalizer & local day bounds
    │   ├── db.hpp            # Session-key isolation (`ATTACH` -> copy `TEMP` tables -> `DETACH`), `v_messages`/`v_chats`/`v_contacts` views, FTS5 & bulk `export`
    │   ├── allowlist.hpp     # Safe file path containment for `send_file` and `send_audio_message`
    │   ├── audit.hpp         # Append-only `mcp-audit.log` writer, `config.json` loader & sliding-window rate limiter
    │   ├── daemon_client.hpp # Unix domain socket client (`CURLOPT_UNIX_SOCKET_PATH`) to `daemon.sock`
    │   ├── app.hpp           # `whatsapp::make_whatsapp_app()` registering all 16 tools & Bugsink DSN
    │   ├── oauth_as.hpp      # OAuth 2.1 + PKCE AS & browser QR/approval flow
    │   ├── qr_svg.hpp        # Standalone QR encoder for browser pairing SVG
    │   └── main.cpp          # Standalone CLI, stdio MCP (`mcp`), and HTTP/OAuth (`http`) entrypoint
    └── tests/
        └── test_main.cpp     # ASan + UBSan tests for `wa_utc`, key isolation, FTS5, views, export & allowlist
```

### Components and Libraries

- **`client/` (Go 1.26):**
  - [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) — WhatsApp Web multi-device protocol client.
  - `github.com/mattn/go-sqlite3` — SQLite driver with FTS5 for `whatsapp.db` and `messages.db`.
  - `github.com/getsentry/sentry-go` — Optional PII-scrubbed error reporting via `BUGSINK_DSN` (disabled when unset).
- **`mcp/` (C++26):**
  - `<mcp.hpp>` & `<reflect.hpp>` — Typed MCP tool definitions, compile-time JSON Schemas, native MCP `image` & `audio` content blocks, and narrow JSON CLI.
  - `<sqlite.hpp>` (`-lsqlite3`) — Read-only SQLite connections, custom `wa_utc` scalar function, `TEMP` table/view isolation, and `iterateLimited` (`PRAGMA query_only = ON`).
  - `<curl.hpp>` (`-lcurl`) — Unix domain socket HTTP calls (`CURLOPT_UNIX_SOCKET_PATH`) to `daemon.sock`.
  - `<json.hpp>` (`glaze`) — Fast JSON serialization.
  - `<bugsink.hpp>` (`-lcurl`) — Optional error reporting via `BUGSINK_DSN` (disabled when unset).
  - `<test.hpp>` (`doctest`) — Unit test suite compiled with `-fsanitize=address,undefined`.

### Runtime Data Directory (`~/.local/share/whatsapp-client/`)

Override with `WHATSAPP_CLIENT_DATA_DIR` (falls back to `WHATSAPP_MCP_DATA_DIR` for compatibility):

| Path | Mode | Purpose |
| --- | --- | --- |
| `~/.local/share/whatsapp-client/` | `0700` | Owner-only data directory |
| `…/whatsapp.db` | `0600` | `whatsmeow` device session, Signal keys, contact names (`whatsmeow_contacts`), and LID mappings (`whatsmeow_lid_map`) |
| `…/messages.db` | `0600` | Synced chats, messages, group participants, and `messages_fts` FTS5 index (WAL mode) |
| `…/daemon.sock` | `0600` | Unix domain socket for `/api/send` and `/api/download` |
| `…/daemon.log` | `0600` | Sync daemon log (auto-trimmed at 5 MiB) |
| `…/mcp-audit.log` | `0600` | Append-only audit log of every MCP/CLI tool invocation |
| `…/config.json` | `0600` | Optional MCP policy overrides (`read_only`, `disabled_tools`, `mutating_tools_per_min`, `whatsapp_send_max_runes`) |
| `…/downloads/` | `0700` | Media files downloaded by `download_media` |

## Safe Single-Daemon Cutover (`whatsapp-mcp` → `whatsapp-client`)

> **Critical rule:** Never run two WhatsApp daemons against the same session simultaneously — WhatsApp will immediately invalidate the device session or corrupt the ratcheting Signal keys in `whatsapp.db`.

1. **Stop and unload the v1 LaunchAgent first:**
   ```bash
   launchctl bootout gui/$(id -u)/com.bdombro.whatsapp-mcp
   # Verify the process has exited and daemon.sock is removed:
   pgrep -fl whatsapp-mcp
   ```
2. **Migrate the data directory in-place (preserves pairing & full message history):**
   ```bash
   mv ~/.local/share/whatsapp-mcp ~/.local/share/whatsapp-client
   ```
3. **Build and start `whatsapp-client` from this workspace:**
   ```bash
cd assistant2/plugins/whatsapp-mcp && just cutover
client/dist/whatsapp-client status
   ```

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

Then install and build from the repository root:

```bash
brew install cmake ninja just llvm node ruby autoconf automake libtool pkgconf go
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
go version
BUGSINK_DSN="" just test
just build
```

The libhv link is a one-time step: if it exists, verify that it points to the
chosen checkout's `ports/libhv` before skipping the command. Never overwrite
a conflicting port directory or modify installed headers/caches. Current HTTP
builds need both local slimr overlays and the separately pinned libhv port;
the older published registry baseline alone is not an HTTP build substitute.
`mcp/` installs vcpkg dependencies into `mcp/build/deps/`, including static
SQLite/FTS5, curl and OpenSSL; no separate Homebrew library installs are needed.
Go must satisfy [`client/go.mod`](./client/go.mod); its dependencies are
resolved automatically by `go test`/`go build`, without vcpkg.

Bootstrapping puts the executable at `$VCPKG_ROOT/vcpkg`, with no `bin/`
subdirectory. Keg-only Homebrew Ruby supplies `/opt/homebrew/opt/ruby/bin/ruby`.
Homebrew LLVM supplies lint tooling; select a different compatible `CXX`
before first configure if needed, since CMake caches it. For reproducible
vcpkg tooling, check out the tested full `VCPKG_REF` commit before bootstrapping;
package registry baselines pin resolution separately.
These commands do not start or pair the client, install LaunchAgents, or
modify production session/history data. Never start a second sync daemon
against an already-used session.

## Building & Testing Both Components

Use Go from `client/go.mod` for the unchanged companion build, and Clang/C++26 with libc++ FAST hardening, CMake 3.31+, Ninja, [just](https://just.systems), [vcpkg](https://vcpkg.io), and compatible clang-tidy for `mcp/`. Set `VCPKG_ROOT` to a bootstrapped vcpkg checkout and put `vcpkg` on `PATH`:

```bash
export VCPKG_OVERLAY_PORTS="$VCPKG_ROOT/local-ports"
```

The root justfile delegates Go commands to `client/` and CMake/Ninja commands to `mcp/`. The MCP manifest installs once under `mcp/build/deps/`, automatically before configure/build/test/lint; CMake manifest auto-install is disabled. CMake imported slimr targets use installed headers and static SQLite with FTS5, never source-checkout include overrides or assumed system SQLite. Debug sanitizer and Release IPO graphs live under `mcp/build/cmake/`. Both enable libc++ FAST hardening and zero automatic-variable initialization; Debug uses ASan and fatal UBSan. No binaries are installed globally and no services are started by validation.

```bash
just test     # Go client tests and C++ MCP tests with ASan + UBSan
just build    # client/dist/whatsapp-client and mcp/build/bin/whatsapp-mcp; run Release C++ tests
just lint     # Root .clang-tidy analyzes mcp/src and mcp/tests
just test-mcp # Inspector 2.14.0 initialize + tools/list only, isolated from production data
just run --help # Run the existing Release MCP binary without rebuilding
```

See [`mcp/README.md`](./mcp/README.md#building--testing) for the C++ toolchain and isolated discovery details. Linux/macOS PR CI runs both components, including Go tests using `client/go.mod`, and requires the tested full `VCPKG_REF` commit variable and uses the pinned public slimr registry without a dependency token. Local macOS validation does not claim Linux execution or remote merge-gate enforcement. The `whatsapp-client` daemon identity, LaunchAgent label, and runtime data paths are unchanged by the MCP app rename.
