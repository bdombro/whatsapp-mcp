# WhatsApp plugin top-level tasks
set shell := ["zsh", "-f", "-eu", "-o", "pipefail", "-c"]
set positional-arguments

# List recipes
_:
    @just --list

# Run tests across client (Go) and mcp (C++26)
test:
    @echo "=== Running Go client tests ==="
    just --justfile client/justfile test
    @echo "=== Running C++26 MCP tests ==="
    just --justfile mcp/justfile test

# Build binaries for both client and mcp
build:
    @echo "=== Building Go client ==="
    just --justfile client/justfile build
    @echo "=== Building C++26 MCP ==="
    just --justfile mcp/justfile build

deps:
    just --justfile mcp/justfile deps

configure configuration="Debug":
    just --justfile mcp/justfile configure {{configuration}}

lint:
    just --justfile mcp/justfile lint

test-mcp:
    just --justfile mcp/justfile test-mcp

run *args:
    ./mcp/build/bin/whatsapp-mcp "$@"

# Clean build artifacts
clean:
    just --justfile client/justfile clean
    just --justfile mcp/justfile clean

# Check daemon and MCP status
status:
    @echo "=== Daemon Status ==="
    @if [ -x client/dist/whatsapp-client ]; then \
        client/dist/whatsapp-client status; \
    elif command -v whatsapp-client >/dev/null 2>&1; then \
        whatsapp-client status; \
    else \
        echo "whatsapp-client not found in client/dist or PATH"; \
    fi
    @echo "=== MCP Status ==="
    @if [ -x mcp/build/bin/whatsapp-mcp ]; then \
        mcp/build/bin/whatsapp-mcp status '{"json":true}'; \
    elif command -v whatsapp-mcp >/dev/null 2>&1; then \
        whatsapp-mcp status '{"json":true}'; \
    else \
        echo "whatsapp-mcp not built or installed in PATH"; \
    fi

# Install release Homebrew formula (pass "whatsapp-mcp whatsapp-client" to include client)
install *formulas:
    node scripts/dev-formula.mjs install {{formulas}}

# Stage local binary tarball into bdombro/whatsapp-mcp tap and install (default: whatsapp-mcp)
install-local *formulas:
    node scripts/dev-formula.mjs install-local {{formulas}}

# Stop whatsapp-mcp if running, reinstall from local build, and restart if previously running
reinstall-local *formulas:
    node scripts/dev-formula.mjs reinstall-local {{formulas}}

# Start/stop/restart only the whatsapp-mcp HTTP service (never spawns duplicate whatsapp-client)
start:
    HOMEBREW_NO_AUTO_UPDATE=1 brew services start bdombro/whatsapp-mcp/whatsapp-mcp

stop:
    HOMEBREW_NO_AUTO_UPDATE=1 brew services stop bdombro/whatsapp-mcp/whatsapp-mcp

restart:
    HOMEBREW_NO_AUTO_UPDATE=1 brew services restart bdombro/whatsapp-mcp/whatsapp-mcp

uninstall:
    HOMEBREW_NO_AUTO_UPDATE=1 brew uninstall bdombro/whatsapp-mcp/whatsapp-mcp

# Perform safe single-daemon cutover from com.bdombro.whatsapp-mcp to com.bdombro.whatsapp-client
cutover: build
    @scripts/cutover.sh
