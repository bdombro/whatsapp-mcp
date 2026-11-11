# AGENTS.md (`whatsapp-mcp`)

Written for: AI coding agents working in the `whatsapp-mcp` plugin repository (`client/` Go sync daemon + `mcp/` C++26 MCP & CLI).

## Commands

| Task | Command |
| --- | --- |
| Run Go `client/` tests + C++26 `mcp/` ASan/UBSan tests | `just test` |
| Build both `client/dist/whatsapp-client` and `mcp/build/bin/whatsapp-mcp` | `just build` |
| Static analysis of all C++ app/test units | `just lint` |
| Isolated MCP startup/discovery | `just test-mcp` |
| Clean build artifacts | `just clean` |

## Repository Split & Responsibilities

| Folder | Language | Owns | Must Not |
| --- | --- | --- | --- |
| [`client/`](./client/AGENTS.md) | Go 1.26 (`whatsmeow`) | 24/7 WhatsApp Web sync into `messages.db`, QR pairing (`login`), LaunchAgent lifecycle (`com.bdombro.whatsapp-client`), 2FA OTP scrubbing, and `daemon.sock` (`/api/send`, `/api/download`) | Serve MCP or expose a TCP port |
| [`mcp/`](./mcp/AGENTS.md) | C++26 (`slimr-cpp`) | All 15 `whatsapp_*` MCP/CLI tools, cryptographic session-key isolation (`DETACH whatsapp.db`), `v_messages`/`v_chats`/`v_contacts` views, `wa_utc`, rate limiting, `mcp-audit.log`, and `daemon.sock` client | Write to `whatsapp.db` or `messages.db` |

## Critical Safety Rules

1. **Never run two WhatsApp daemons simultaneously:** Only one process may ever open `whatsapp.db` for a WhatsApp session. Before starting or cutting over `whatsapp-client`, confirm `com.bdombro.whatsapp-mcp` is unloaded and no `whatsapp-mcp` or `whatsapp-client` daemon process is running.
2. **Never send messages or download media against real contacts in tests:** Ask the operator explicitly before invoking `send_message`, `send_file`, `send_audio_message`, `download_media`, `start`, `stop`, `restart`, `login`, or `reset` against the live `~/.local/share/whatsapp-client/` directory.
3. **Schema compatibility:** Never incompatibly alter the schema of `whatsapp.db` (owned by `whatsmeow`) or `messages.db`.
4. **Cryptographic session-key isolation in `mcp/`:** Every read connection in `mcp/src/db.hpp` must `ATTACH` `whatsapp.db` read-only, copy only `t_names`, `t_contacts`, and `t_lid_map` into `TEMP` tables, and **immediately `DETACH` `whatsapp.db`** before creating `v_messages`, `v_chats`, `v_contacts` and setting `PRAGMA query_only = ON`.

## Build Architecture

The root justfile delegates Go commands unchanged to `client/` and CMake/Ninja commands to `mcp/`. The root `.clang-tidy` covers `mcp/src` and `mcp/tests`. Native Linux/macOS CI runs Go tests/build plus C++ Debug/Release tests, lint, and isolated Inspector discovery. Keep macOS LaunchAgent deployment separate from portable tests; never start the daemon during validation.

## C++ Memory Safety

- Immediately wrap owned C handles, allocations, descriptors, and temporary resources in RAII. Make owning wrappers noncopyable unless copying creates independent ownership; moves must leave the source safely destructible.
- Borrow views, pointers, and references only for the current call by default. Store owning values unless lifetime/invalidation guarantees are explicit. Deferred callbacks require explicit ownership-safe captures; no escaping reference captures without a demonstrated lifetime guarantee.
- Initialize scalar/pointer members at declaration and local values before use. Compiler auto-initialization is defense in depth, not permission for uninitialized reads.
- Check range/overflow before narrowing lengths/counts at C API boundaries; casts alone are not validation. Report invalid inputs using existing error mechanisms.
- Never let C++ exceptions escape C callbacks. Translate them into the C API's error/abort mechanism and propagate failures at the C++ boundary; do not silently swallow them.
- Preserve hardening, zero auto-initialization, ASan/UBSan with fatal reports, and required lint/tests. These checks do not prove memory safety.
