# whatsapp-mcp: Developer Reference

User installation, configuration and compatibility status are in the
[README](../README.md). This reference preserves implementation details,
alternative transport schemas and validation commands. A client configuration
example is not a claim of verified July/OAuth interoperability.

Current HTTP source builds use local slimr and libhv overlays. The pinned
published registry baseline predates these HTTP changes; do not claim that
baseline builds the current HTTP app without overrides. CI configuration is
not evidence that a runner/platform/client has passed.

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
  - `modernc.org/sqlite` — Go SQLite driver for the sync databases.
  - `github.com/getsentry/sentry-go` — Optional PII-scrubbed error reporting via `BUGSINK_DSN` (disabled when unset).
- **`mcp/` (C++26):**
  - `<mcp.hpp>` & `<reflect.hpp>` — Typed MCP tool definitions, compile-time JSON Schemas, native MCP `image` & `audio` content blocks, and narrow JSON CLI.
  - `<sqlite.hpp>` (`-lsqlite3`) — Read-only SQLite connections, custom `wa_utc` scalar function, `TEMP` table/view isolation, and `iterateLimited` (`PRAGMA query_only = ON`).
  - `<curl.hpp>` (`-lcurl`) — Unix domain socket HTTP calls (`CURLOPT_UNIX_SOCKET_PATH`) to `daemon.sock`.
  - `<json.hpp>` (`glaze`) — Fast JSON serialization.
  - `<bugsink.hpp>` (`-lcurl`) — Optional error reporting via `BUGSINK_DSN` (disabled when unset).
  - `<test.hpp>` (`doctest`) — Unit test suite compiled with `-fsanitize=address,undefined`.

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
just cutover
client/dist/whatsapp-client status
   ```

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
just test-mcp # July MCP stdio + ephemeral HTTP discovery, isolated from production data
just run --help # Run the existing Release MCP binary without rebuilding
```

See the [MCP developer reference](../mcp/docs/development.md#building--testing) for the C++ toolchain and isolated discovery details. CI is configured for both components, including Go tests using `client/go.mod`, and requires the tested full `VCPKG_REF` commit variable. Current July HTTP consumers require matching slimr/libhv overlays until a corresponding registry release is published. Local macOS validation does not claim Linux execution or remote merge-gate enforcement. The `whatsapp-client` daemon identity, LaunchAgent label, and runtime data paths are unchanged by the MCP app rename.
