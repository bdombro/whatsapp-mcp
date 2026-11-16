# AGENTS.md (`whatsapp-mcp/mcp`)

Written for: AI coding agents working in the `whatsapp-mcp/mcp` C++26 MCP server and CLI.

`whatsapp-mcp/mcp` is a standalone C++26 application built on the `slimr-*` libraries (`slimr::mcp` + `slimr::mcp_http`) providing all 16 WhatsApp MCP tools (`list_chats`, `list_messages`, `search_contacts`, `get_chat`, `get_direct_chat_by_contact`, `get_contact_chats`, `get_message_context`, `get_last_interaction`, `send_message`, `send_file`, `send_audio_message`, `download_media`, `export`, `sql`, `schema`, `status`) and CLI commands against `$(brew --prefix)/var/whatsapp-client/messages.db` and `daemon.sock` (`WHATSAPP_CLIENT_DATA_DIR` overrides).

Over Streamable HTTP (`whatsapp-mcp http`, default `0.0.0.0:10008`; OAuth `publicBaseUrl` stays `127.0.0.1`), it enforces OAuth 2.1 + PKCE (`S256`) with dynamic client registration (`POST /register`), RFC 9728 (`/.well-known/oauth-protected-resource`), RFC 8414 (`/.well-known/oauth-authorization-server`), browser QR pairing + explicit CSRF-protected approval (`GET /authorize`, `GET /authorize/qr`, `POST /authorize/approve`, `POST /authorize/cancel`), `POST /token` (refresh rotation + replay family revocation), and `POST /revoke`.


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

Release, install and `daemon-*` live in the repository-root justfile (two formulas); `CHANGELOG.md` and `VERSION` are at the repository root.

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

CMake declares targets/dependencies; Ninja owns incremental builds. Dependencies resolve from the pinned `slimr-*` registries in `vcpkg-configuration.json` (no overlays) and install under `build/deps/`; Debug sanitizer and Release trees live under `build/cmake/`. SQLite is statically linked with FTS5. `test-mcp` checks July discovery over stdio and ephemeral HTTP (OAuth-protected) with Bun against a scratch data dir; it does not call tools, open user databases, or connect to/start the daemon.

<!-- cpp-templates:begin -->
## Tooling alignment

Align this repository via the `cpp-templates` skill (`just check <template> .`). Keep repository-specific rules outside `<!-- cpp-templates:begin -->` and `<!-- cpp-templates:end -->
