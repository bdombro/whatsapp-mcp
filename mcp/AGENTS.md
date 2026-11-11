# AGENTS.md (`whatsapp-mcp/mcp`)

Written for: AI coding agents working in the `whatsapp-mcp/mcp` C++26 MCP server and CLI.

`whatsapp-mcp/mcp` is a standalone C++26 application built on `slimr-cpp` (`slimr::mcp` + `slimr::mcp_http`) providing all 16 WhatsApp MCP tools (`list_chats`, `list_messages`, `search_contacts`, `get_chat`, `get_direct_chat_by_contact`, `get_contact_chats`, `get_message_context`, `get_last_interaction`, `send_message`, `send_file`, `send_audio_message`, `download_media`, `export`, `sql`, `schema`, `status`) and CLI commands against `~/.local/share/whatsapp-client/messages.db` and `daemon.sock`.

Over Streamable HTTP (`whatsapp-mcp http --host 127.0.0.1 --port 8088`), it enforces OAuth 2.1 + PKCE (`S256`) with dynamic client registration (`POST /register`), RFC 9728 (`/.well-known/oauth-protected-resource`), RFC 8414 (`/.well-known/oauth-authorization-server`), browser QR pairing + explicit CSRF-protected approval (`GET /authorize`, `GET /authorize/qr`, `POST /authorize/approve`, `POST /authorize/cancel`), `POST /token` (refresh rotation + replay family revocation), and `POST /revoke`.

## Commands

| Task | Command |
| --- | --- |
| Run unit & isolation tests (ASan + UBSan) | `just test` |
| Build release binary (`build/bin/whatsapp-mcp`) | `just build` |
| Static analysis (root `.clang-tidy`) | `just lint` |
| Isolated MCP startup/discovery | `just test-mcp` |
| Clean build artifacts | `just clean` |

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

CMake declares targets/dependencies; Ninja owns incremental builds. Just orchestrates automatic `deps` before configure/build/test/lint; CMake manifest auto-install is disabled. Set `VCPKG_ROOT` and `VCPKG_OVERLAY_PORTS` in the environment. Dependencies install under `build/deps/`; Debug sanitizer and Release trees live under `build/cmake/`. Use installed slimr targets, never source-checkout include overrides. SQLite is statically linked with FTS5. `run` does not rebuild.

Use a Clang/C++26 toolchain with libc++ FAST hardening, CMake, Ninja, vcpkg, just, and compatible clang-tidy. Inspector uses pinned npm tooling and an isolated server environment; it does not call tools, open user databases, or connect to/start the daemon.

## C++ Memory Safety

- Immediately wrap owned C handles, allocations, descriptors, and temporary resources in RAII. Make owning wrappers noncopyable unless copying creates independent ownership; moves must leave the source safely destructible.
- Borrow views, pointers, and references only for the current call by default. Store owning values unless lifetime/invalidation guarantees are explicit. Deferred callbacks require explicit ownership-safe captures; no escaping reference captures without a demonstrated lifetime guarantee.
- Initialize scalar/pointer members at declaration and local values before use. Compiler auto-initialization is defense in depth, not permission for uninitialized reads.
- Check range/overflow before narrowing lengths/counts at C API boundaries; casts alone are not validation. Report invalid inputs using existing error mechanisms.
- Never let C++ exceptions escape C callbacks. Translate them into the C API's error/abort mechanism and propagate failures at the C++ boundary; do not silently swallow them.
- Preserve hardening, zero auto-initialization, ASan/UBSan with fatal reports, and required lint/tests. These checks do not prove memory safety.
