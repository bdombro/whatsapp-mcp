# whatsapp-mcp: Contributing

User installation, configuration and compatibility status are in the
[README](../README.md). This reference preserves implementation details,
alternative transport schemas and validation commands. A client configuration
example is not a claim of verified July/OAuth interoperability.

Dependencies resolve directly from the pinned `slimr-down-now/*` vcpkg git
registries in `mcp/vcpkg-configuration.json` (no local overlays). CI
configuration is not evidence that a runner/platform/client has passed.

<!-- cpp-templates:begin -->
## Contents

- [Develop and test](#develop-and-test)
- [Unreleased libraries](#unreleased-libraries)
- [Rules](#rules)
- [PR, CI, and release flow](#pr-ci-and-release-flow)
- [Dependency bumps](#dependency-bumps)

## Develop and test

```bash
just doctor
just test
just lint
```

Iterate on a single doctest case with `just test "<case>"` (Debug build with ASan + UBSan, `BUGSINK_DSN=""`). For `homebrew-mcp` repos, run `just test-mcp` as well.

## Unreleased libraries

vcpkg reads extra port dirs from `VCPKG_OVERLAY_PORTS`, and an overlay wins over the registry. Point it at a dir of symlinks, one per unreleased lib; manifests never change:

```sh
mkdir -p ~/.vcpkg/overlays/dev
ln -s ~/dev/slimr/<lib>/ports/slimr-<lib> ~/.vcpkg/overlays/dev/slimr-<lib>
export VCPKG_OVERLAY_PORTS=~/.vcpkg/overlays/dev
just test
```

Remove the symlink (or `unset VCPKG_OVERLAY_PORTS`) to build against the released lib again. Keep the export out of shell startup files.

## Rules

- Never push directly to `main`; all changes land through squash-merged pull requests.
- PRs touch no release files (`VERSION`, `CHANGELOG.md`, `versions/`, `Formula/*.rb`, `Casks/*.rb`).
- Write the PR title (plus optional body) as the changelog entry; squash-merge turns it into one commit on `main`.
- Sanitize everything (no secrets, tokens, DSNs, or personal data in commits, logs, or docs).
- Run `just test` (ASan + UBSan) and `just lint` before opening or updating a PR.

## PR, CI, and release flow

- Every PR runs `test.yml`: a `gate` job skips macOS/Linux builds on docs-only PRs, and a single `ci` summary job gates merge.
- Merging to `main` triggers `release.yml` (one release at a time). It collects squash commits since the last tag (skipping docs-only ranges), picks the bump from merged PR labels (`patch` default, or `release:minor` / `release:major`; agents must ask before adding labels), writes `VERSION`/`vcpkg.json`, `CHANGELOG.md` (and `vcpkg x-add-version` for libraries), commits as the GitHub App, tags `vX.Y.Z`, and publishes the GitHub Release (plus assets and `Formula/*.rb` / `Casks/*.rb` updates).
- Pre-releases are a known option (tag/channel), not currently built.

## Dependency bumps

Bump a library baseline in `vcpkg-configuration.json` only when a bug fix, new API, or security fix requires it; otherwise wait for the next `just upgrade-deps`.
<!-- cpp-templates:end -->

## App Architecture

The `client/` is a Go daemon using `whatsmeow`; the stateless `mcp/` server and JSON CLI are implemented in C++26 on the `slimr-*` libraries.

```text
plugins/whatsapp-mcp/
├── VERSION                   # Shared version compiled into both binaries
├── CHANGELOG.md              # Release notes (## Unreleased)
├── justfile                  # Delegates to client/ and mcp/; both brew services (daemon-*), install, cutover
├── Formula/                  # whatsapp-client.rb and whatsapp-mcp.rb (macOS/Linux, service + env file loader)
├── .github/
│   ├── actions/setup/action.yml     # Toolchain, Go, vcpkg bootstrap + binary cache
│   └── workflows/{test,release}.yml # Go + C++ tests per platform; release packages both binaries
├── scripts/
│   ├── release.ts            # Version bump, changelog promotion, both formulas, commit/tag/gh-release
│   ├── formula-shared.ts     # Formula/changelog parsing and SHA-256 helpers
│   └── cutover.sh            # Legacy LaunchAgent cutover (`whatsapp-mcp` -> `whatsapp-client`)
├── client/                   # Go 1.26 `whatsmeow` sync daemon (`whatsapp-client`)
│   ├── justfile              # `just test`, `just build`
│   ├── main.go, cmd.go       # Entrypoint & daemon/login/start/stop/status/reset CLI
│   ├── daemon.go, system.go  # LaunchAgent (`com.bdombro.whatsapp-client.plist`) management
│   ├── core/                 # Data dir/brew prefix, 2FA OTP redaction, log rotation, Bugsink (`sentry-go`)
│   └── whatsapp/             # `whatsmeow` sync, `messages.db` store, `daemon.sock` (`/api/send`, `/api/download`)
└── mcp/                      # C++26 MCP server & narrow JSON CLI (`whatsapp-mcp`), aligned to cpp-templates `homebrew-mcp`
    ├── vcpkg.json            # Dependencies (slimr-mcp[http], slimr-sqlite, ...) installed under build/deps/
    ├── vcpkg-configuration.json # Pinned vcpkg builtin and slimr-* git registries
    ├── CMakeLists.txt        # App/test targets, VERSION define, hardening, sanitizers
    ├── justfile              # CMake/Ninja configure, test, build, fmt, lint, doctor, test-mcp
    ├── scripts/              # doctor.sh, test-mcp.ts (Bun)
    ├── src/
    │   ├── schemas.hpp       # Reflected input structs for all tools
    │   ├── time_util.hpp     # Deterministic `wa_utc` timestamp normalizer & local day bounds
    │   ├── db.hpp            # Session-key isolation (`ATTACH` -> copy `TEMP` tables -> `DETACH`), views, FTS5 & bulk `export`
    │   ├── allowlist.hpp     # Safe file path containment for `send_file` and `send_audio_message`
    │   ├── audit.hpp         # Append-only `mcp-audit.log` writer, `config.json` loader & sliding-window rate limiter
    │   ├── daemon_client.hpp # Data dir resolution and Unix socket client (`AF_UNIX`) to `daemon.sock`
    │   ├── app.hpp           # `whatsapp::make_whatsapp_app()` registering all 16 tools & Bugsink DSN
    │   ├── oauth_as.hpp      # OAuth 2.1 + PKCE AS & browser QR/approval flow
    │   ├── qr_svg.hpp        # Standalone QR encoder for browser pairing SVG
    │   └── main.cpp          # Standalone CLI and HTTP/OAuth (`http`) entrypoint
    └── tests/
        └── test_main.cpp     # ASan + UBSan tests for `wa_utc`, key isolation, FTS5, views, export & allowlist
```

### Components and Libraries

- **`client/` (Go 1.26):**
  - [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) — WhatsApp Web multi-device protocol client.
  - `modernc.org/sqlite` — Go SQLite driver for the sync databases.
  - `github.com/getsentry/sentry-go` — Optional PII-scrubbed error reporting via `BUGSINK_DSN` (disabled when unset).
- **`mcp/` (C++26, `slimr-*`):**
  - `<mcp.hpp>`, `<mcp_http.hpp>` & `<reflect.hpp>` — Typed MCP tool definitions, compile-time JSON Schemas, native MCP `image` & `audio` content blocks, and narrow JSON CLI.
  - `<sqlite.hpp>` (static SQLite with FTS5) — Read-only SQLite connections, custom `wa_utc` scalar function, `TEMP` table/view isolation, and `iterateLimited` (`PRAGMA query_only = ON`).
  - `<httplib.h>` (`cpp-httplib`) — Unix domain socket HTTP calls (`AF_UNIX`) to `daemon.sock`.
  - `<json.hpp>` (`glaze`) — Fast JSON serialization.
  - `<bugsink.hpp>` — Optional error reporting via `BUGSINK_DSN` (disabled when unset), release `whatsapp-mcp@<VERSION>`.
  - `<test.hpp>` (`doctest`) — Unit test suite compiled with `-fsanitize=address,undefined`.

## `.mcp.json` Configuration

`whatsapp-mcp/mcp` runs standalone over Streamable HTTP (default bind `127.0.0.1:10008`, `http://127.0.0.1:10008/mcp`, with OAuth 2.1 + PKCE and a browser QR/approval UI) or direct CLI (`whatsapp-mcp <tool> ['{"arg":"val"}']`), exposing canonical unprefixed tool names (`list_chats`, `get_chat`, `list_messages`, `export`, `sql`, `schema`, `send_message`, `status`, …).

To configure `whatsapp-mcp` over HTTP in `.mcp.json`:

```json
{
  "mcpServers": {
    "whatsapp-mcp": {
      "type": "http",
      "url": "http://127.0.0.1:10008/mcp"
    }
  }
}
```

## Legacy Cutover

> **Critical rule:** Never run two WhatsApp daemons against the same session simultaneously — WhatsApp will immediately invalidate the device session or corrupt the ratcheting Signal keys in `whatsapp.db`.

`just cutover` runs `scripts/cutover.sh`, the one-off migration from the v1
`com.bdombro.whatsapp-mcp` LaunchAgent to the `com.bdombro.whatsapp-client`
LaunchAgent. It predates the Homebrew services and keeps its legacy paths.
Neither app migrates data: moving an existing data directory into
`$(brew --prefix)/var/whatsapp-client` (or pointing `WHATSAPP_CLIENT_DATA_DIR`
at it) is a deliberate operator step with every daemon stopped.

## Building & Testing

Prerequisites (`just doctor` checks them, plus Go):

- **macOS:** Homebrew `llvm@23` (`clang`/`clang++` with `-stdlib=libc++`, static `libc++` in Release and vcpkg deps), `cmake`, `ninja`, `just`, `bun`, `gh`, `jq`, `go` (per `client/go.mod`), and bootstrapped `vcpkg` (`VCPKG_ROOT` set).
- **Linux (`ubuntu-24.04`):** Homebrew on Linux (`llvm@23` provides `clang++` with `-stdlib=libc++`, static libc++ in Release builds), `ninja`, `just`, `bun`, `gh`, `jq`, `go`, bootstrapped `vcpkg`, and `sudo loginctl enable-linger "$USER"` for `brew services`.

Set `LLVM_BIN` to override the `$(brew --prefix llvm@23)/bin` path used by `just fmt` and `just lint`. Set `CXX` before first configure to override the compiler; CMake caches it.

```bash
just doctor            # Verify tools, C++26 compiler, LLVM_BIN, VCPKG_ROOT, Go, and Linux linger
just test              # Go client tests + C++ Debug ASan/UBSan tests (BUGSINK_DSN="")
just build             # client/dist/whatsapp-client and mcp/build/bin/whatsapp-mcp (Release, no tests)
just fmt               # Format C++, Go, and justfiles
just lint              # Format checks + clang-tidy (mcp/.clang-tidy)
just test-mcp          # Bun ephemeral HTTP MCP 2026-07-28 discovery, isolated from production data
just upgrade-deps      # Update vcpkg registry baselines and Go modules, then run tests
just install           # Clean-install both released formulas, brew test them, start both services
just daemon-status     # brew services info for both formulas (also daemon-start/stop/restart/logs)
just clean             # Remove build outputs only; leaves user data untouched
```

`mcp/` installs vcpkg dependencies under `mcp/build/deps/` automatically before configure/build/test/lint; CMake manifest auto-install is disabled. Debug sanitizer and Release IPO graphs live under `mcp/build/cmake/`; only Release binaries are emitted to `mcp/build/bin/`. Go resolves its modules during `go test`/`go build` without vcpkg. Building and testing never start a service, pair a session, or touch production data.

For local development without touching the installed `whatsapp-mcp` service (`10008`), run `./mcp/build/bin/whatsapp-mcp http --port 20008` (dev endpoint `http://127.0.0.1:20008/mcp`) against a scratch `WHATSAPP_CLIENT_DATA_DIR`.

See the [MCP contributor reference](../mcp/docs/contributing.md#building--testing) for C++ details. CI (`test.yml`, `release.yml`) runs on `macos-26` and `ubuntu-24.04` on pull requests and merges to `main`; local macOS validation is not Linux execution evidence.
