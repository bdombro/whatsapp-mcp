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
- [Maintainer setup](#maintainer-setup)
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

## Maintainer setup

Direct pushes to `main` are blocked; `release.yml` uses a dedicated GitHub App so it can commit release metadata and formula updates without giving humans or agents an admin bypass.

### 1. Create and install the release GitHub App

Create one GitHub App (Settings -> Developer settings -> GitHub Apps):

- **Homepage URL:** any URL (for example, the owner's GitHub profile)
- **Callback / Setup URLs:** blank; **Webhook Active:** unchecked
- **Repository permissions:** `Contents: Read and write`, `Metadata: Read-only`
- **Where can this GitHub App be installed?:** Any account

Record `<app-id>`, generate a private key, and save it locally if needed for manual recovery (it can always be regenerated):

```sh
chmod 600 ~/.ssh/<app-name>.private-key.pem
```

Install the App on each owner (org or user) and select the target repositories.

### 2. Store Actions secrets

Org-level (requires org owner and `admin:org` scope):

```sh
gh auth refresh -h github.com -s admin:org
gh secret set RELEASE_APP_ID --org <owner> --visibility all --body "<app-id>"
gh secret set RELEASE_APP_PRIVATE_KEY --org <owner> --visibility all < ~/.ssh/<app-name>.private-key.pem
gh secret list --org <owner>
```

Per-repo (for user-owned repos or selected repos):

```sh
gh secret set RELEASE_APP_ID -R <owner>/<repo> --body "<app-id>"
gh secret set RELEASE_APP_PRIVATE_KEY -R <owner>/<repo> < ~/.ssh/<app-name>.private-key.pem
gh secret list -R <owner>/<repo>
```

`gh secret list` prints secret names only.

### 3. Protect `main`

Create (or `PUT /repos/<owner>/<repo>/rulesets/<ruleset-id>` to update) the `main` ruleset with App-only bypass, required `ci` check, squash-only merges, and no force push or deletion, then enforce squash-only merges on the repo:

```sh
gh api -X POST /repos/<owner>/<repo>/rulesets --input - <<'JSON'
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] }
  },
  "bypass_actors": [
    { "actor_id": <app-id>, "actor_type": "Integration", "bypass_mode": "always" }
  ],
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": false,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": false,
        "allowed_merge_methods": ["squash"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": false,
        "required_status_checks": [{ "context": "ci" }]
      }
    }
  ]
}
JSON

gh api -X PATCH /repos/<owner>/<repo> \
  -F allow_squash_merge=true \
  -F allow_merge_commit=false \
  -F allow_rebase_merge=false

gh api /repos/<owner>/<repo>/rulesets
```

### 4. Act as the App (emergency fix or manual release step)

Use [`scripts/release-bot`](https://github.com/slimr-down-now/cpp-templates/blob/main/scripts/release-bot) with `RELEASE_APP_ID` and `RELEASE_APP_KEY` set (under the hood it signs a short-lived `RS256` App JWT, exchanges it for an installation token, and revokes the token on exit):

```sh
export RELEASE_APP_ID=<app-id>
export RELEASE_APP_KEY=~/.ssh/<app-name>.private-key.pem

release-bot push-main                  # push HEAD to origin main as the App
release-bot gh release create ...      # run one gh command with GH_TOKEN set
release-bot shell                      # subshell with GH_TOKEN; revokes on exit
```

Flags before the subcommand: `--owner <owner>` (token scope; defaults to `origin` owner) and `--repo <owner>/<repo>` (`push-main` target; defaults to `origin`).

## Dependency bumps

Bump a library baseline in `vcpkg-configuration.json` only when a bug fix, new API, or security fix requires it; otherwise wait for the next `just upgrade-deps`.
<!-- cpp-templates:end -->

## App Architecture

The `client/` is a Go daemon using `whatsmeow`; the stateless `mcp/` server is implemented in C++26 on the `slimr-*` libraries.

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
└── mcp/                      # C++26 MCP server (`whatsapp-mcp`), aligned to cpp-templates `homebrew-mcp`
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
    │   └── main.cpp          # Standalone HTTP/OAuth (`http`) entrypoint
    └── tests/
        └── test_main.cpp     # ASan + UBSan tests for `wa_utc`, key isolation, FTS5, views, export & allowlist
```

### Components and Libraries

- **`client/` (Go 1.26):**
  - [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) — WhatsApp Web multi-device protocol client.
  - `modernc.org/sqlite` — Go SQLite driver for the sync databases.
  - `github.com/getsentry/sentry-go` — Optional PII-scrubbed error reporting via `BUGSINK_DSN` (disabled when unset).
- **`mcp/` (C++26, `slimr-*`):**
  - `<mcp.hpp>`, `<mcp_http.hpp>` & `<reflect.hpp>` — Typed MCP tool definitions, compile-time JSON Schemas, native MCP `image` & `audio` content blocks, and HTTP `/tools/<name>` endpoints.
  - `<sqlite.hpp>` (static SQLite with FTS5) — Read-only SQLite connections, custom `wa_utc` scalar function, `TEMP` table/view isolation, and `iterateLimited` (`PRAGMA query_only = ON`).
  - `<httplib.h>` (`cpp-httplib`) — Unix domain socket HTTP calls (`AF_UNIX`) to `daemon.sock`.
  - `<json.hpp>` (`glaze`) — Fast JSON serialization.
  - `<bugsink.hpp>` — Optional error reporting via `BUGSINK_DSN` (disabled when unset), release `whatsapp-mcp@<VERSION>`.
  - `<test.hpp>` (`doctest`) — Unit test suite compiled with `-fsanitize=address,undefined`.

## `.mcp.json` Configuration

`whatsapp-mcp/mcp` runs standalone over Streamable HTTP (default bind `127.0.0.1:10008`, `http://127.0.0.1:10008/mcp` plus `/tools/<name>` and `/tools/openapi.json` with OAuth 2.1 + PKCE and a browser QR/approval UI), exposing canonical unprefixed tool names (`list_chats`, `get_chat`, `list_messages`, `export`, `sql`, `schema`, `send_message`, `status`, …).

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
