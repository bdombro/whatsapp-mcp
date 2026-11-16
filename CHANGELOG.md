# Changelog

## Unreleased

- Read daemon data and socket from $(brew --prefix)/var/whatsapp-client instead of ~/.local/share/whatsapp-{client,mcp}
- Add --log-file <path> option with 10 MiB log rotation to <path>.1 (mode 0600)
- HTTP accepts any --host and an ephemeral --port 0 (slimr-mcp update)
- Internal: align build, CI, Homebrew formulas, and documentation with cpp-templates
