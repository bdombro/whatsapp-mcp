# WhatsApp MCP: Developer Reference

[User setup](../README.md) | [Repository setup](../../README.md)

## App Architecture

The stateless MCP server and JSON CLI are implemented in C++26 on `slimr-cpp`. It reads `messages.db` and uses the companion Go `whatsapp-client` only for sends and media downloads.

## Cryptographic Session-Key Isolation & SQLite Views

`whatsapp.db` (managed by `whatsmeow`) holds both contact names/LID mappings and sensitive Signal protocol session keys. Before running **any** query (including arbitrary user/agent queries in `sql`), `src/db.hpp` prepares an isolated read-only connection:

1. Opens `messages.db` with `SQLITE_OPEN_READONLY` and registers the deterministic `wa_utc(timestamp)` C++ scalar function (normalizing RFC3339, Go `time.String()`, and ISO timestamps to UTC `YYYY-MM-DD HH:MM:SS`).
2. `ATTACH`es `whatsapp.db` in `mode=ro`.
3. Copies only non-sensitive contact metadata (`t_names`, `t_contacts`, `t_lid_map`) into in-memory `TEMP` tables.
4. Immediately **`DETACH`es `whatsapp.db`** so Signal private keys, identity keys, and session ratchets are completely unreachable from the SQLite connection.
5. Creates `TEMP` views `v_messages`, `v_chats`, and `v_contacts` (resolving `@lid` and `@s.whatsapp.net` JIDs to human-readable contact and chat names).
6. Enables `PRAGMA query_only = ON`.

## HTTP Transport, OAuth 2.1 & Browser Connect UX

When launched via `whatsapp-mcp http --host 127.0.0.1 --port 8088` (default port `8088`), the server exposes `POST /mcp` protected by OAuth 2.1 + PKCE (`S256`):
- `GET /.well-known/oauth-protected-resource` (RFC 9728) & `GET /.well-known/oauth-authorization-server` (RFC 8414)
- `POST /register` (RFC 7591 dynamic client registration restricted to loopback redirect URIs)
- `GET /authorize` + `GET /authorize/qr` + `POST /authorize/approve` + `POST /authorize/cancel` (CSRF-protected browser Connect page; renders live QR SVG when `whatsapp-client` is unpaired, or shows the already-paired JID for explicit owner approval without touching `whatsapp.db`)
- `POST /token` (authorization code + PKCE `S256` + refresh token rotation with replay family revocation) and `POST /revoke` (revokes MCP access without disconnecting the WhatsApp session)

### CLI Examples

```bash
# Search messages across all chats using FTS5 BM25
./build/bin/whatsapp-mcp list_messages '{"query":"boiler","limit":10}'

# Bulk export messages since a local date
./build/bin/whatsapp-mcp export '{"since":"2026-11-01"}'

# Run a read-only SQL query against the isolated views
./build/bin/whatsapp-mcp sql '{"sql":"SELECT chat_name, COUNT(*) AS n FROM v_messages GROUP BY chat_jid ORDER BY n DESC LIMIT 10"}'

# Inspect the SQL view schema
./build/bin/whatsapp-mcp schema
```

## Building & Testing

Build this directory with Clang/C++26 and libc++ FAST hardening, CMake 3.31+, Ninja, [just](https://just.systems), [vcpkg](https://vcpkg.io), and compatible clang-tidy. Set `VCPKG_ROOT` to a bootstrapped checkout and make `vcpkg` available on `PATH`; configure the local overlay explicitly:

```bash
export VCPKG_OVERLAY_PORTS="$VCPKG_ROOT/local-ports"
```

`just configure`, `test`, `build`, and `lint` automatically run `deps`, installing manifest dependencies under `build/deps/`. CMake uses installed `slimr::mcp_http` and `slimr::sqlite` targets with static SQLite/FTS5; manifest auto-install is disabled. Local overlay ports must already be registered; no source-checkout include overrides or global executable installs are used. Registry publication is separate.

Debug sanitizers and Release IPO builds have separate `build/cmake/Debug/` and `build/cmake/Release/` graphs. Both enable libc++ hardening and zero automatic-variable initialization; Debug adds ASan and fatal UBSan. Set `CXX` before the first configure to override the default Apple compiler on macOS or `clang++` on Linux; subsequent configures preserve CMake's cached compiler even when clang-tidy is added to `PATH`. CTest runs database and mock-socket fixtures with a finite timeout and isolated repo-local HOME/XDG/temporary directories. Release builds run those tests too.

```bash
just test     # Run C++26 unit & isolation tests (wa_utc, key isolation, FTS5, views, export, mock socket) with ASan + UBSan
just build    # Build/test Release binary at build/bin/whatsapp-mcp
just lint     # Analyze app and test units with the root .clang-tidy and compile_commands.json
just test-mcp # July MCP stdio + ephemeral HTTP discovery (requires Node)
just run --help # Run the existing Release binary without rebuilding
```

`scripts/test-mcp.mjs` runs the server with an isolated HOME/XDG environment, no external CLI PATH or credentials, and Bugsink disabled; it never opens production databases, invokes tools, or starts/connects to the daemon. The repository-root Linux/macOS workflow is also configured for Go tests/build. CI configuration requires the tested full `VCPKG_REF` commit variable. Current July HTTP builds require matching local slimr/libhv overlays until a corresponding registry release is published. Linux execution and remote merge-gate enforcement are not claimed by local macOS verification.
