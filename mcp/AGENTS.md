# AGENTS.md (`whatsapp-mcp/mcp`)

Written for: AI coding agents working in the `whatsapp-mcp/mcp` C++26 MCP server and CLI.

`whatsapp-mcp/mcp` is a standalone C++26 application built on the `slimr-*` libraries (`slimr::mcp` + `slimr::mcp_http`) providing all 16 WhatsApp MCP tools (`list_chats`, `list_messages`, `search_contacts`, `get_chat`, `get_direct_chat_by_contact`, `get_contact_chats`, `get_message_context`, `get_last_interaction`, `send_message`, `send_file`, `send_audio_message`, `download_media`, `export`, `sql`, `schema`, `status`) and CLI commands against `$(brew --prefix)/var/whatsapp-client/messages.db` and `daemon.sock` (`WHATSAPP_CLIENT_DATA_DIR` overrides).

Over Streamable HTTP (`whatsapp-mcp http`, default `127.0.0.1:10008`; OAuth `publicBaseUrl` stays `127.0.0.1`), it enforces OAuth 2.1 + PKCE (`S256`) with dynamic client registration (`POST /register`), RFC 9728 (`/.well-known/oauth-protected-resource`), RFC 8414 (`/.well-known/oauth-authorization-server`), browser QR pairing + explicit CSRF-protected approval (`GET /authorize`, `GET /authorize/qr`, `POST /authorize/approve`, `POST /authorize/cancel`), `POST /token` (refresh rotation + replay family revocation), and `POST /revoke`.


## Documentation Upkeep

When a change conflicts with `README.md`, update the affected README and its
linked user/developer documentation in the same change. Keep examples,
configuration, install/build commands and compatibility claims consistent
with implemented behavior; do not claim unverified release or client support.

## Commands

| Task | Command |
| --- | --- |
| Run unit & isolation tests (ASan + UBSan) | `just test ["<filter>"]` |
| Build release binary (`build/bin/whatsapp-mcp`, no tests) | `just build` |
| Format / static analysis (`.clang-tidy`) | `just fmt`, `just lint` |
| Isolated MCP startup/discovery | `just test-mcp` |
| Clean build artifacts | `just clean` |

Install and `daemon-*` live in the repository-root justfile (two formulas); release runs on merge to `main`, and `CHANGELOG.md` and `VERSION` live at the repository root.

## Rules

1. **Strict Session-Key Isolation (`src/db.hpp`):**
   - Open `messages.db` read-only (`SQLITE_OPEN_READONLY`).
   - Register deterministic scalar function `wa_utc`.
   - `ATTACH DATABASE '.../whatsapp.db' AS wa_TempDb` read-only.
   - Copy only contact names (`t_names`), contacts (`t_contacts`), and LID mappings (`t_lid_map`) into `TEMP` tables.
   - Immediately `DETACH DATABASE wa_TempDb` so `whatsmeow` Signal private keys and session ratchets cannot be read by any subsequent SQL query (including `sql`).
   - Create `TEMP` views `v_messages`, `v_chats`, `v_contacts` and enforce `PRAGMA query_only = ON`.
2. **File Send Containment (`src/allowlist.hpp`):** `send_file` and `send_audio_message` must only accept regular files under `$HOME` or `/tmp`/`$TMPDIR`, rejecting symlinks, hidden paths (`~/.ssh`, `~/.config`), and `~/Library`.
3. **Audit & Rate Limiting (`src/audit.hpp`):** Every tool call must append a record to `mcp-audit.log`, and mutating tools must enforce `mutating_tools_per_min` and `read_only` / `disabled_tools` from `config.json`.

## Build Architecture

CMake declares targets/dependencies; Ninja owns incremental builds. Dependencies resolve from the pinned `slimr-*` registries in `vcpkg-configuration.json` (no overlays) and install under `build/deps/`; Debug sanitizer and Release trees live under `build/cmake/`. SQLite is statically linked with FTS5. `test-mcp` checks July discovery over ephemeral HTTP (OAuth-protected) with Bun against a scratch data dir; it does not call tools, open user databases, or connect to/start the daemon.

<!-- cpp-templates:begin -->
<!-- cpp-templates:commit c746b02c9f5b17b63c4921e6adff7fa8c5f6dc37 -->
## Contributing

Read the sections of [docs/contributing.md](docs/contributing.md) relevant to your change before editing.

## Tooling alignment

Align this repository via the `cpp-templates` skill, which diffs `base/` and `templates/<template>/` since the `<!-- cpp-templates:commit <sha> -->` marker above and adapts changes while preserving repository customizations. Keep repository-specific rules outside `<!-- cpp-templates:begin -->` and `<!-- cpp-templates:end -->`.

## Edit-don't-rewrite

- Make targeted edits and move files with `git mv`; never regenerate files from scratch or retype them from memory.
- Never delete, skip, or loosen tests to pass; if a test must change, explain why in the commit message.

## C++ Memory Safety

- Immediately wrap owned C handles, allocations, descriptors, and temporary resources in RAII. Make owning wrappers noncopyable unless copying deliberately creates independent ownership; moves must leave the source safely destructible.
- Borrow views, pointers, and references only for the current call by default. Store owning values unless lifetime/invalidation guarantees are explicit. Deferred callbacks require explicit ownership-safe captures; no escaping reference captures without a demonstrated lifetime guarantee.
- Initialize scalar/pointer members at declaration and local values before use. Compiler zero-initialization is defense in depth, not permission for uninitialized reads.
- Check range/overflow before narrowing lengths/counts at C API boundaries; casts alone are not validation. Report failures through existing error mechanisms.
- Never let C++ exceptions escape C callbacks. Translate them into the C API's error/abort mechanism and propagate failures at the C++ boundary; do not silently swallow them.
- Preserve libc++ hardening, zero auto-initialization, ASan/UBSan with fatal reports, and required lint/tests. These checks do not prove memory safety.

## C++ style

- Favor modern C++26 and the standard library over legacy patterns; write for readability; comment what is not obvious and why.
- Prefer values over pointers, `const` by default, and no raw `new`/`delete`.
- Use `std::expected` for expected failures; use exceptions only at process and callback boundaries.
- Structure code with one folder per feature under `src/`, mirrored under `tests/`. Keep one test TU per executable (`tests/test_main.cpp`).
- Name what you use (`using slimr::Json;` or `slimr::Json`, never `using namespace`). Use PascalCase for types and camelCase for functions and variables (review guidance, not lint).
- Put file-local helpers in an unnamed namespace in `.cpp` files, or in `detail::` in headers.
- Follow KISS: inline single-use helpers, avoid speculative abstractions, keep diffs lean.
- Apply style rules to new and touched code; ports do not rewrite working code just for style.
- `.clang-tidy` enforces bug-finding checks (`clang-analyzer-core.*`, `clang-analyzer-cplusplus.*`, and bug-risk `bugprone-*` checks; pending legacy findings are temporarily disabled with per-check counts in `.clang-tidy`); fix findings or suppress inline with a reason.
- `INTERFACE_SOURCES` caveat: linking a header-only library that ships an implementation TU (such as `slimr-yaml`) into both a library and an executable that links that library compiles the TU twice and causes duplicate symbols.

## Testing

Iterate with `just test "<case>"` (Debug build with ASan + UBSan, `BUGSINK_DSN=""`), then finish with a full `just test`. `just build` compiles Release only and runs no tests.

## Recipes

- Core: `build`, `clean`, `configure`, `deps`, `doctor`, `fmt`, `lint`, `test [filter]`, `upgrade-deps`
- `homebrew-*`: `install`, `uninstall`
- `homebrew-service` and `homebrew-mcp`: `daemon-logs`, `daemon-restart`, `daemon-start`, `daemon-status`, `daemon-stop`
- `homebrew-mcp`: `test-mcp`
- Dropped tooling (do not re-add): foreground runner, local tap/overlay symlinking, GitHub-branch/local-publish/dry-run helpers, standalone formula check, `release` recipe, and JS formula helpers

## Changelog

PRs touch no release files (`CHANGELOG.md`, `VERSION`, `versions/`). Write the PR title (and optional body) as the changelog entry; `release.yml` generates `CHANGELOG.md` from squash-merge commits after merge.

## Paths, env vars, and README

- `<name>` in Homebrew paths is always the formula name, never a short XDG name: app data lives in `$(brew --prefix)/var/<name>`, config in `$(brew --prefix)/etc/<name>/`, and service secrets in `$(brew --prefix)/etc/<name>/env` (`chmod 600`, never in the public formula).
- Secrets are env vars only.
- Every `README.md` is end-user first: what it does, install, usage, env var table (name, required or optional, purpose), then links into `docs/` (`docs/contributing.md`, `docs/architecture.md`) for contributor workflow and architecture.
<!-- cpp-templates:end -->
