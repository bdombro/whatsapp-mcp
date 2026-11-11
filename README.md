# whatsapp-mcp

```
   .----------------.
  /  chats + tools   \
 |    <>       <>     |
  \_______  ________/
          \/
```

Search and review WhatsApp chats, export message history, and send messages or media through one MCP. A separate sync client keeps the WhatsApp session connected and the local message database current.

The split keeps message access tightly scoped: the sync client owns the WhatsApp session, while the MCP queries the message database through isolated, read-only views. Signal keys are detached before queries run, and message bodies are treated as untrusted content.

> **Client compatibility:** MCP **2026-07-28 only**; legacy clients cannot connect.
> The workspace has been exercised with OpenCode v2.2.6 using explicit July
> configuration. Cursor's July support is deferred; Claude Code July runtime
> acceptance is not yet verified. Shipping a plugin manifest is not proof of
> client compatibility.

## Try It

After setup, ask your agent:

> Find the recent messages about the boiler appointment, without sending anything.

## Features

- Search contacts and chats, retrieve message context, run read-only SQL, and export history.
- Send text, files, and voice notes or download media through the local sync client.
- Signal session keys stay isolated from the MCP query connection; write calls are rate-limited and audited.
- Small footprint: only the focused sync daemon stays resident; the MCP is a separate standalone binary.

> **Security & untrusted content:** Like any tool that reads private messages and can send messages, message bodies are treated strictly as untrusted data. One-time passcodes are scrubbed before hitting disk, `whatsapp.db` Signal cryptographic keys are detached before any SQL query executes, file sends are restricted to safe user paths, every mutating tool call is logged to `mcp-audit.log` and rate-limited, and `config.json` can disable write tools entirely (`"read_only": true`).

## Installation

- **Prebuilt release:** the checked-in [Homebrew formula](Formula/whatsapp-mcp.rb)
  currently has a placeholder checksum. It is not a ready-to-install release.
  Check [releases](https://github.com/bdombro/whatsapp-mcp/releases) for published
  binaries and a finalized formula before using a release installation command.
- **Build from source:** the current path is [Development](#development).
  Local slimr overlays are for development, not a runtime requirement.
- **Already installed:** follow the setup and service steps below. Installing a
  Cursor/Claude manifest only configures an endpoint; it does not install or
  start the native service.


## Configure and Pair WhatsApp

The companion `whatsapp-client` must be installed and running to synchronize
messages and handle sends/downloads. **Choose one daemon manager:** the
existing `com.bdombro.whatsapp-client` LaunchAgent or the Homebrew client
service, never both. Check your existing daemon before starting another.

Both components use `~/.local/share/whatsapp-client/`; keep this directory
unchanged. To use another location, configure `WHATSAPP_CLIENT_DATA_DIR`
consistently for both components.

After installing both local binaries, and **only if no other daemon is
running**, choose exactly one manager.

For the custom LaunchAgent:

```bash
whatsapp-client start
```

Alternatively, for Homebrew (do not also run the command above):

```bash
brew services start bdombro/whatsapp-mcp/whatsapp-client
```

Start the MCP HTTP service, then connect with a client supporting both July
MCP and the required OAuth flow. Its browser Connect page shows a WhatsApp QR
code when unpaired and asks for explicit approval when already paired.
Scan through WhatsApp's Linked Devices screen. Pairing alone does not grant
MCP access. This client/OAuth combination still needs runtime acceptance;
do not treat packaged manifests as evidence it works end to end.


## Start and Manage the Service

For an already-installed Homebrew formula:

```bash
brew services start bdombro/whatsapp-mcp/whatsapp-mcp
brew services info whatsapp-mcp
brew services restart bdombro/whatsapp-mcp/whatsapp-mcp
brew services stop bdombro/whatsapp-mcp/whatsapp-mcp
```

The endpoint is `http://127.0.0.1:8088/mcp`; logs are under
`/opt/homebrew/var/log/`. To run in the foreground instead, use
`whatsapp-mcp http` (or `./mcp/build/bin/whatsapp-mcp http` from a source checkout).
Do not run the foreground server and service on the same port.
Keep the default loopback bind; this is not a shared remote-host deployment.


## Connect Your Agent

After starting the service, add this server to OpenCode v2.2.6's
`opencode.jsonc` (merge it with any existing servers):

```json
{
  "mcp": {
    "servers": {
      "whatsapp-mcp": {
        "type": "remote",
        "url": "http://127.0.0.1:8088/mcp",
        "protocol": "2026-07-28"
      }
    }
  }
}
```

Use either an explicit server entry or a plugin manifest, not both.
Alternative client schemas and stdio configuration are in the
[developer reference](docs/development.md). The compatibility notice above
applies to every transport.


## Runtime Data

Override with `WHATSAPP_CLIENT_DATA_DIR` (falls back to `WHATSAPP_MCP_DATA_DIR` for compatibility):

| Path | Mode | Purpose |
| --- | --- | --- |
| `~/.local/share/whatsapp-client/` | `0700` | Owner-only data directory |
| `…/whatsapp.db` | `0600` | `whatsmeow` device session, Signal keys, contact names (`whatsmeow_contacts`), and LID mappings (`whatsmeow_lid_map`) |
| `…/messages.db` | `0600` | Synced chats, messages, group participants, and `messages_fts` FTS5 index (WAL mode) |
| `…/daemon.sock` | `0600` | Unix domain socket for `/api/send` and `/api/download` |
| `…/daemon.log` | `0600` | Sync daemon log (auto-trimmed at 5 MiB) |
| `…/mcp-audit.log` | `0600` | Append-only audit log of every MCP/CLI tool invocation |
| `…/config.json` | `0600` | Optional MCP policy overrides (`read_only`, `disabled_tools`, `mutating_tools_per_min`, `whatsapp_send_max_runes`) |
| `…/downloads/` | `0700` | Media files downloaded by `download_media` |

## Troubleshooting

- **Client cannot connect:** confirm it supports July MCP, the service is
  running, and the client URL matches the configured port. OpenCode needs
  `"protocol": "2026-07-28"`; old `initialize` clients will fail.
- **Port occupied:** inspect the listener and stop only the service/process
  you own, or deliberately choose another port and update the client URL.
- **Works in a terminal, not as a service:** launchd does not inherit
  `~/.zshrc`. Configure the service environment separately or use the
  plugin's supported private configuration file; inspect
  `/opt/homebrew/var/log/` for errors.
- **Connect or pairing fails:** verify July and OAuth client support, then inspect the existing daemon status. Never fix this by starting a second daemon or deleting session databases.
- **Source build cannot find vcpkg or slimr ports:** load the development
  environment, bootstrap vcpkg, and register both slimr links and the pinned
  libhv overlay. Never repair this by editing installed headers.
- **Compiler/hardening check fails:** upgrade the Xcode toolchain; set
  `CXX` before first configure. CMake caches the compiler, so an intentional
  compiler change needs a fresh configuration tree.


## Development

This section is for building or changing the server, not configuring an
installed binary. [Architecture, transport reference and test details](docs/development.md)
are separate from the user setup flow.

<details>
<summary>macOS source-build setup (vcpkg and local slimr overlays)</summary>

## macOS Build Prerequisites (vcpkg setup)

Install [Homebrew](https://brew.sh) and current Xcode Command Line Tools
(`xcode-select --install`; finish installation before continuing), or full Xcode.
Apple Clang 21 is the verified C++26/libc++ toolchain; older installations need
upgrading. These instructions assume Apple Silicon Homebrew at `/opt/homebrew`.

Add this environment snippet to `~/.zshrc`, then open a new terminal (or run
`source ~/.zshrc`). Use `~/.zshrc`, not `~/.zshenv`, to avoid changing
non-interactive zsh scripts:

`local-ports` is the dedicated vcpkg overlay directory for local development
linking: `just publish --local --all` registers symlinks to your slimr-cpp checkout,
overriding published packages without making a registry release. See the
[publishing guide on slimr-cpp](https://github.com/bdombro/slimr-cpp/blob/main/docs/publishing.md#local-environment).

```bash
export VCPKG_ROOT="$HOME/.local/share/vcpkg"
export VCPKG_OVERLAY_PORTS="$VCPKG_ROOT/local-ports"
# Use your existing workspace checkout instead when available.
export SLIMR_CPP="${SLIMR_CPP:-$HOME/dev/bdombro/slimr-cpp}"
export CXX="/usr/bin/clang++"
export CLANG_TIDY="/opt/homebrew/opt/llvm/bin/clang-tidy"
export PATH="$VCPKG_ROOT:/opt/homebrew/opt/ruby/bin:/opt/homebrew/bin:$PATH"
```

Then install and build from the repository root:

```bash
brew install cmake ninja just llvm node ruby autoconf automake libtool pkgconf go
mkdir -p "$(dirname "$VCPKG_ROOT")"
if [ ! -d "$VCPKG_ROOT/.git" ]; then
  git clone https://github.com/microsoft/vcpkg.git "$VCPKG_ROOT"
fi
"$VCPKG_ROOT/bootstrap-vcpkg.sh" -disableMetrics
mkdir -p "$(dirname "$SLIMR_CPP")"
if [ ! -d "$SLIMR_CPP/.git" ]; then
  git clone https://github.com/bdombro/slimr-cpp.git "$SLIMR_CPP"
fi
(cd "$SLIMR_CPP" && just publish --local --all)
ln -s "$SLIMR_CPP/ports/libhv" "$VCPKG_OVERLAY_PORTS/libhv"

"$CXX" --version
vcpkg version
go version
BUGSINK_DSN="" just test
just build
```

The libhv link is a one-time step: if it exists, verify that it points to the
chosen checkout's `ports/libhv` before skipping the command. Never overwrite
a conflicting port directory or modify installed headers/caches. Current HTTP
builds need both local slimr overlays and the separately pinned libhv port;
the older published registry baseline alone is not an HTTP build substitute.
`mcp/` installs vcpkg dependencies into `mcp/build/deps/`, including static
SQLite/FTS5, curl and OpenSSL; no separate Homebrew library installs are needed.
Go must satisfy [`client/go.mod`](./client/go.mod); its dependencies are
resolved automatically by `go test`/`go build`, without vcpkg.

Bootstrapping puts the executable at `$VCPKG_ROOT/vcpkg`, with no `bin/`
subdirectory. Keg-only Homebrew Ruby supplies `/opt/homebrew/opt/ruby/bin/ruby`.
Homebrew LLVM supplies lint tooling; select a different compatible `CXX`
before first configure if needed, since CMake caches it. For reproducible
vcpkg tooling, check out the tested full `VCPKG_REF` commit before bootstrapping;
package registry baselines pin resolution separately.
These commands do not start or pair the client, install LaunchAgents, or
modify production session/history data. Never start a second sync daemon
against an already-used session.

</details>

After the source build succeeds, stage and install the local Homebrew formula:

```bash
just install-local whatsapp-mcp whatsapp-client
```

Installing a formula and starting its service are separate actions.
For a source-only foreground run, use
`./mcp/build/bin/whatsapp-mcp http` from the repository root.
