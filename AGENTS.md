# AGENTS.md (`whatsapp-mcp`)

Written for: AI coding agents working in the `whatsapp-mcp` plugin repository (`client/` Go sync daemon + `mcp/` C++26 MCP & CLI).


## Documentation Upkeep

When a change conflicts with `README.md`, update the affected README and its
linked user/developer documentation in the same change. Keep examples,
configuration, install/build commands and compatibility claims consistent
with implemented behavior; do not claim unverified release or client support.

## Commands

| Task | Command |
| --- | --- |
| Run Go `client/` tests + C++26 `mcp/` ASan/UBSan tests | `just test` |
| Build both `client/dist/whatsapp-client` and `mcp/build/bin/whatsapp-mcp` (no tests) | `just build` |
| Format C++, Go and justfiles | `just fmt` |
| Format checks + clang-tidy (`mcp/.clang-tidy`) | `just lint` |
| Isolated MCP startup/discovery | `just test-mcp` |
| Verify toolchain | `just doctor` |
| Update vcpkg baselines and Go modules | `just upgrade-deps` |
| Release both formulas via `release.yml` | `just release <patch\|minor\|major>` |
| Both brew services (only when asked) | `just install`, `just daemon-{start,stop,restart,status,logs}` |
| Clean build artifacts | `just clean` |

## Repository Split & Responsibilities

| Folder | Language | Owns | Must Not |
| --- | --- | --- | --- |
| [`client/`](./client/AGENTS.md) | Go 1.26 (`whatsmeow`) | 24/7 WhatsApp Web sync into `messages.db`, QR pairing (`login`), LaunchAgent lifecycle (`com.bdombro.whatsapp-client`), 2FA OTP scrubbing, and `daemon.sock` (`/api/send`, `/api/download`) | Serve MCP or expose a TCP port |
| [`mcp/`](./mcp/AGENTS.md) | C++26 (`slimr-*`, `homebrew-mcp` template) | All 16 MCP/CLI tools (`list_chats`, …, `sql`, `schema`, `status`), Streamable HTTP (`0.0.0.0:10008`, OAuth metadata on `127.0.0.1`) + OAuth 2.1 + PKCE + browser QR pairing/approval UI, cryptographic session-key isolation (`DETACH whatsapp.db`), `v_messages`/`v_chats`/`v_contacts` views, `wa_utc`, rate limiting, `mcp-audit.log`, and `daemon.sock` client | Write to `whatsapp.db` or `messages.db` |
| [`Formula/`](./Formula/) | Ruby (Homebrew) | Sibling formulas `whatsapp-client.rb` (Go sync daemon) and `whatsapp-mcp.rb` (C++26 HTTP MCP service on port `10008`), released together from root `VERSION` by `scripts/release.ts` — kept independent so restarting `whatsapp-mcp` never spawns a duplicate sync daemon | Run two daemons against one `whatsapp.db` |
| [`.cursor-plugin/`](./.cursor-plugin/), [`.claude-plugin/`](./.claude-plugin/) | JSON manifests | Plugin metadata pointing to `http://127.0.0.1:10008/mcp` | Embed stdio wrappers or credentials |

## Critical Safety Rules

1. **Never run two WhatsApp daemons simultaneously:** Only one process may ever open `whatsapp.db` for a WhatsApp session. Before starting or cutting over `whatsapp-client`, confirm `com.bdombro.whatsapp-mcp` is unloaded and no `whatsapp-mcp` or `whatsapp-client` daemon process is running.
2. **Never send messages or download media against real contacts in tests:** Ask the operator explicitly before invoking `send_message`, `send_file`, `send_audio_message`, `download_media`, `start`, `stop`, `restart`, `login`, or `reset` against the live `$(brew --prefix)/var/whatsapp-client/` directory.
3. **Schema compatibility:** Never incompatibly alter the schema of `whatsapp.db` (owned by `whatsmeow`) or `messages.db`.
4. **Cryptographic session-key isolation in `mcp/`:** Every read connection in `mcp/src/db.hpp` must `ATTACH` `whatsapp.db` read-only, copy only `t_names`, `t_contacts`, and `t_lid_map` into `TEMP` tables, and **immediately `DETACH` `whatsapp.db`** before creating `v_messages`, `v_chats`, `v_contacts` and setting `PRAGMA query_only = ON`.

## Build Architecture

`mcp/` is checked against the cpp-templates `homebrew-mcp` template (`just check homebrew-mcp plugins/whatsapp-mcp/mcp` from the workspace). The root justfile, root `.github/`, `scripts/{release,formula-shared}.ts`, `Formula/`, `VERSION`, `CHANGELOG.md` and Go `client/` stay repo-owned: the root delegates to `client/` and `mcp/`, and its `daemon-*`, `install` and `uninstall` recipes act on both formulas. Apps read `$(brew --prefix)/var/whatsapp-client` (or `WHATSAPP_CLIENT_DATA_DIR`) and never migrate data. Keep LaunchAgent and brew service deployment separate from portable tests; never start a daemon or service during validation.

<!-- cpp-templates:begin -->
## Tooling alignment

Align this repository via the `cpp-templates` skill (`just check <template> .`). Keep repository-specific rules outside `<!-- cpp-templates:begin -->` and `<!-- cpp-templates:end -->`. Do not hand-edit verbatim template files or template recipes (repositories may add extra recipes or variables).

## Edit-don't-rewrite

- Make targeted edits and move files with `git mv`; never regenerate files from scratch or retype them from memory.
- Template files arrive through `just new` or `just sync`.
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

- Core: `build`, `clean`, `configure`, `deps`, `doctor`, `fmt`, `lint`, `release`, `test [filter]`, `upgrade-deps`
- `homebrew-*`: `install`, `uninstall`
- `homebrew-service` and `homebrew-mcp`: `daemon-logs`, `daemon-restart`, `daemon-start`, `daemon-status`, `daemon-stop`
- `homebrew-mcp`: `test-mcp`
- Dropped tooling (do not re-add): foreground runner, local tap/overlay symlinking, GitHub-branch/local-publish/dry-run helpers, standalone formula check, and JS formula helpers

## Changelog

Add one line under `## Unreleased` in `CHANGELOG.md` for every change (use `Internal: ...` for internal changes).

## Paths, env vars, and README

- `<name>` in Homebrew paths is always the formula name, never a short XDG name: app data lives in `$(brew --prefix)/var/<name>`, config in `$(brew --prefix)/etc/<name>/`, and service secrets in `$(brew --prefix)/etc/<name>/env` (`chmod 600`, never in the public formula).
- Secrets are env vars only.
- Every `README.md` is end-user first: what it does, install, usage, env var table (name, required or optional, purpose), then links into `docs/` (`docs/development.md`, `docs/architecture.md`) for contributor workflow and architecture.

## Remote tests

Never run `test.yml` manually before `just release` (because `release` runs `test.yml` first). Run `test.yml` manually only when asked or to reproduce a Linux-only failure.
<!-- cpp-templates:end -->
