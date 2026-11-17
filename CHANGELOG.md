# Changelog

## Unreleased

- Drop per-tool CLI from `whatsapp-mcp` (server-only `http` mode); move HTTP tool routes and OpenAPI spec from root (`/openapi.json`, `/<tool>`) to `/tools/<tool>` and `/tools/openapi.json` (on by default) with `/mcp` OAuth audience parity

## v3.1.1 - 2026-11-17

- Default HTTP bind to `127.0.0.1:10008` (pass `--host 0.0.0.0` to expose on all interfaces)
- Serve OpenAPI 3.1.0 spec at `GET /openapi.json` and `--openapi` (via `slimr-serve` v0.3.0 and `slimr-mcp` v0.2.1)
- Internal: pin `slimr-serve` v0.3.0 and `slimr-mcp` v0.2.1, add `/openapi.json` smoke check, and document `overlay-ports` workflow

## v3.1.0 - 2026-11-16

- Internal: strip overlay-ports and pin slimr-mcp v0.2.0 and slimr-serve v0.1.1 baselines
- Migrate `whatsapp-mcp` to `slimr-serve` typed `OutputStruct` handlers via `slimr-mcp` adapter and drop stdio `mcp` subcommand (HTTP-only MCP + narrow JSON CLI)
- Fix direct CLI tool calls (`<bin> <tool> '{...}'`), disabled by default since `slimr-mcp` v0.1.4
- Internal: replace slimr-curl/libhv with cpp-httplib (AF_UNIX daemon client) and upgrade slimr-mcp/slimr-bugsink
- Internal: switch macOS build to llvm@23 with static libc++

## v3.0.0 - 2026-11-16

- OAuth authorization endpoints and pairing pages only accept loopback connections (403 from LAN peers)
- Embed VERSION into both binaries and tag Bugsink events with <name>@<version>
- Bind HTTP server to 0.0.0.0:10008 by default; OAuth metadata still advertises 127.0.0.1 (pass --host 127.0.0.1 for loopback-only)
- Read daemon data and socket from $(brew --prefix)/var/whatsapp-client instead of ~/.local/share/whatsapp-{client,mcp}
- Add --log-file <path> option with 10 MiB log rotation to <path>.1 (mode 0600)
- HTTP accepts any --host and an ephemeral --port 0 (slimr-mcp update)
- Internal: align build, CI, Homebrew formulas, and documentation with cpp-templates
- Internal: resolve slimr registries from the `slimr-down-now` org
