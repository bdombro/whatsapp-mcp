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

inspect:
    just --justfile mcp/justfile inspect

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
        client/dist/whatsapp-client status || true; \
    elif command -v whatsapp-client >/dev/null 2>&1; then \
        whatsapp-client status || true; \
    elif command -v whatsapp-mcp >/dev/null 2>&1; then \
        whatsapp-mcp status || true; \
    else \
        echo "Neither whatsapp-client nor whatsapp-mcp installed in PATH"; \
    fi
    @echo "=== MCP Status ==="
    @if [ -f mcp/build/bin/whatsapp-mcp ]; then \
        mcp/build/bin/whatsapp-mcp status '{"json":true}'; \
    fi

# Perform safe single-daemon cutover from com.bdombro.whatsapp-mcp to com.bdombro.whatsapp-client
cutover: build
    @scripts/cutover.sh
