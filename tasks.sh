#!/usr/bin/env bash
# Project tasks (replaces justfile). Run from repository root.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$ROOT/whatsapp-cli"

usage() {
	local code="${1:-1}"
	cat <<'EOF'
Usage: ./tasks.sh <command>

Commands:
  build       Build whatsapp-cli (FTS5) into whatsapp-cli/whatsapp-cli.bin
  update      build + install binary to /usr/local/bin + restart daemon if running
  install     update + login + install-daemon + zsh completions
  test        Run tests with coverage summary (fuzzy, mcp packages)
  test-cover  Run tests and open HTML coverage report
  reset       whatsapp-cli reset && whatsapp-cli login
EOF
	exit "$code"
}

cmd_build() {
	(cd "$CLI_DIR" && go build -tags "sqlite_fts5" -o whatsapp-cli.bin .)
}

cmd_update() {
	cmd_build
	sudo cp "$CLI_DIR/whatsapp-cli.bin" /usr/local/bin/whatsapp-cli
	sudo chmod +x /usr/local/bin/whatsapp-cli
	echo "Installed /usr/local/bin/whatsapp-cli"

	if whatsapp-cli info 2>/dev/null | grep -q "Status:     running"; then
		whatsapp-cli restart
		echo "Restarted core daemon"
	fi
}

cmd_install() {
	cmd_update
	whatsapp-cli login
	whatsapp-cli install-daemon

	local comp_line='eval "$(whatsapp-cli completions zsh)"'
	if ! grep -qF "$comp_line" ~/.zshrc 2>/dev/null; then
		echo "" >> ~/.zshrc
		echo "$comp_line" >> ~/.zshrc
		echo "Added shell completions to ~/.zshrc (restart your terminal or run: source ~/.zshrc)"
	else
		echo "Shell completions already in ~/.zshrc"
	fi
}

cmd_test() {
	(
		cd "$CLI_DIR"
		go test -tags "sqlite_fts5" -coverprofile=coverage.out -count=1 ./...
		echo ""
		echo "=== Coverage summary ==="
		go tool cover -func=coverage.out | grep -E "^(whatsapp-client/(fuzzy|mcp)|total)"
	)
}

cmd_test_cover() {
	(
		cd "$CLI_DIR"
		go test -tags "sqlite_fts5" -coverprofile=coverage.out -count=1 ./...
		go tool cover -html=coverage.out
	)
}

cmd_reset() {
	whatsapp-cli reset
	whatsapp-cli login
}

case "${1:-}" in
	build) cmd_build ;;
	update) cmd_update ;;
	install) cmd_install ;;
	test) cmd_test ;;
	test-cover) cmd_test_cover ;;
	reset) cmd_reset ;;
	-h | --help) usage 0 ;;
	"")
		echo "Error: missing command" >&2
		usage 1
		;;
	*)
		echo "Unknown command: $1" >&2
		usage 1
		;;
esac
