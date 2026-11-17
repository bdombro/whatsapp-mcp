# WhatsApp plugin: whatsapp-client (Go sync daemon) + whatsapp-mcp (C++26 MCP server)
set shell := ["bash", "-eu", "-o", "pipefail", "-c"]
set positional-arguments

formulas := "whatsapp-client whatsapp-mcp"
platforms := "macos linux"
required := "brew bun cmake gh git go jq just ninja vcpkg"

_:
    @just --list

# Compile the Go client into client/dist and the C++ Release binary into mcp/build/bin (no tests)
build:
    just --justfile client/justfile build
    just --justfile mcp/justfile build

# Remove client and mcp build output
clean:
    just --justfile client/justfile clean
    just --justfile mcp/justfile clean

# One-time migration from the legacy com.bdombro.whatsapp-mcp LaunchAgent to com.bdombro.whatsapp-client
cutover: build
    @scripts/cutover.sh

# Follow both service logs
daemon-logs:
    prefix="$(brew --prefix)"; tail -n 100 -f "$prefix/var/log/whatsapp-client.log" "$prefix/var/log/whatsapp-mcp.log"

# Restart both services and wait until whatsapp-mcp is ready
daemon-restart:
    for f in {{ formulas }}; do brew services stop "$f"; done
    for f in {{ formulas }}; do brew services start "$f"; done
    just --justfile mcp/justfile _wait-ready

# Register both services to run at login, start them, and wait until whatsapp-mcp is ready
daemon-start:
    for f in {{ formulas }}; do brew services start "$f"; done
    just --justfile mcp/justfile _wait-ready

# Show both brew service states and probe the live whatsapp-mcp endpoint
daemon-status:
    for f in {{ formulas }}; do brew services info "$f" || true; done
    just --justfile mcp/justfile _probe

# Stop both services and unregister them, so they won't start at login
daemon-stop:
    for f in {{ formulas }}; do brew services stop "$f"; done

# Check required tools (incl. Go), C++26 support and env vars, printing fixes
doctor:
    LLVM_BIN="$(just --justfile mcp/justfile --evaluate llvm)" bash mcp/scripts/doctor.sh {{ required }}

# Format C++ sources, Go sources and justfiles
fmt:
    just --justfile mcp/justfile fmt
    gofmt -w client
    just --fmt --unstable
    just --justfile client/justfile --fmt --unstable

# Clean-install both released binaries from GitHub exactly as users do, test them, then start both services
install: uninstall
    brew tap bdombro/whatsapp-mcp https://github.com/bdombro/whatsapp-mcp
    for f in {{ formulas }}; do brew install "bdombro/whatsapp-mcp/$f"; done
    for f in {{ formulas }}; do brew test "bdombro/whatsapp-mcp/$f"; done
    just daemon-start

# Check formatting, then lint C++ with clang-tidy
lint:
    just --justfile mcp/justfile lint
    test -z "$(gofmt -l client)" || { gofmt -l client; echo "gofmt: run just fmt" >&2; exit 1; }
    just --fmt --check --unstable
    just --justfile client/justfile --fmt --check --unstable

# Release on GitHub (tests per platform, version bump, changelog, build, both formulas, tag), then pull the release commit
release bump:
    gh workflow run release.yml -f bump="$1"
    sleep 5; id="$(gh run list --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId')"; if [ -t 1 ]; then gh run watch --interval 30 --compact --exit-status "$id"; else gh run watch --interval 30 --exit-status "$id" >/dev/null; fi || { gh run view "$id" --log-failed | tail -n 100; exit 1; }
    git pull --ff-only

# Run Go client tests, then the C++ Debug ASan + UBSan tests
test:
    just --justfile client/justfile test
    just --justfile mcp/justfile test

# Isolated MCP HTTP startup check against mcp/build/bin
test-mcp:
    just --justfile mcp/justfile test-mcp

# Stop both services, uninstall both formulas, remove the tap
uninstall:
    for f in {{ formulas }}; do brew services stop "$f" || true; done
    for f in {{ formulas }}; do brew uninstall "$f" || true; done
    brew untap bdombro/whatsapp-mcp || true

# Move vcpkg baselines and Go modules to latest, then run tests (no commit)
upgrade-deps:
    just --justfile mcp/justfile upgrade-deps
    go -C client get -u ./...
    go -C client mod tidy
    just --justfile client/justfile test
