# WhatsApp MCP: Developer Reference

[User setup](../README.md) | [Repository setup](../../README.md)

## App Architecture

The stateless MCP server and JSON CLI are implemented in C++26 on the `slimr-*` libraries, aligned to the cpp-templates `homebrew-mcp` template. It reads `messages.db` and uses the companion Go `whatsapp-client` only for sends and media downloads.

## Cryptographic Session-Key Isolation & SQLite Views

`whatsapp.db` (managed by `whatsmeow`) holds both contact names/LID mappings and sensitive Signal protocol session keys. Before running **any** query (including arbitrary user/agent queries in `sql`), `src/db.hpp` prepares an isolated read-only connection:

1. Opens `messages.db` with `SQLITE_OPEN_READONLY` and registers the deterministic `wa_utc(timestamp)` C++ scalar function (normalizing RFC3339, Go `time.String()`, and ISO timestamps to UTC `YYYY-MM-DD HH:MM:SS`).
2. `ATTACH`es `whatsapp.db` in `mode=ro`.
3. Copies only non-sensitive contact metadata (`t_names`, `t_contacts`, `t_lid_map`) into in-memory `TEMP` tables.
4. Immediately **`DETACH`es `whatsapp.db`** so Signal private keys, identity keys, and session ratchets are completely unreachable from the SQLite connection.
5. Creates `TEMP` views `v_messages`, `v_chats`, and `v_contacts` (resolving `@lid` and `@s.whatsapp.net` JIDs to human-readable contact and chat names).
6. Enables `PRAGMA query_only = ON`.

## HTTP Transport, OAuth 2.1 & Browser Connect UX

When launched via `whatsapp-mcp http [--host <addr>] [--port <n>] [--log-file <path>]` (default bind `127.0.0.1:10008`; OAuth metadata and `publicBaseUrl` advertise `http://127.0.0.1:<port>` unless `--host` names a specific address), the server exposes `POST /mcp` protected by OAuth 2.1 + PKCE (`S256`):
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

Prerequisites, Linux notes and the release flow are in the
[repository developer reference](../../docs/development.md#building--testing);
`just doctor` checks them. Dependencies resolve from the pinned `slimr-*`
registries in `vcpkg-configuration.json` and install under `build/deps/`
automatically before configure/build/test/lint; CMake manifest auto-install is
disabled.

Debug sanitizer and Release IPO graphs live in `build/cmake/Debug/` and `build/cmake/Release/`; only Release binaries are emitted to `build/bin/`. Both enable libc++ FAST hardening and zero automatic-variable initialization; Debug adds ASan and fatal UBSan. CTest runs database and mock-socket fixtures with a finite timeout, `BUGSINK_DSN=""`, and isolated repo-local HOME/XDG/temporary directories and `HOMEBREW_PREFIX`.

```bash
just test              # C++26 unit & isolation tests (wa_utc, key isolation, FTS5, views, export, mock socket) with ASan + UBSan
just test "<filter>"   # Build the test target and run matching doctest cases
just build             # Optimized IPO Release binary at build/bin/whatsapp-mcp (no tests)
just fmt               # Format C++ sources and justfile
just lint              # Format check + clang-tidy (.clang-tidy)
just test-mcp          # Bun ephemeral HTTP MCP 2026-07-28 discovery (OAuth-protected), isolated
just clean             # Remove build/ only; leaves user data untouched
```

For local development without touching the installed service (`10008`), run `./build/bin/whatsapp-mcp http --port 20008` with a scratch `WHATSAPP_CLIENT_DATA_DIR`.

`scripts/test-mcp.ts` runs the server against a scratch `WHATSAPP_CLIENT_DATA_DIR` with Bugsink disabled; it never opens production databases, invokes tools, or starts/connects to the daemon.

## Develop against an unreleased lib

Add `"overlay-ports"` to the consumer's `vcpkg-configuration.json`, then run `just test` or `just build` as usual (add one entry per unreleased lib):

```json
{
  "overlay-ports": [
    "<relative path to ~/dev/slimr/<name>/ports/slimr-<name>>"
  ]
}
```

Remove `"overlay-ports"` and bump the registry baseline once the lib is released. Never commit or release with `overlay-ports` set.
