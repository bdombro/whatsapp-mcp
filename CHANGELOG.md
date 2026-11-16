# Changelog

## Unreleased

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
