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

Align this repository via the `cpp-templates` skill (`just check <template> .`). Keep repository-specific rules outside `<!-- cpp-templates:begin -->` and `<!-- cpp-templates:end -->
