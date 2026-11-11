#!/usr/bin/env bash
set -euo pipefail

LABEL_OLD="com.bdombro.whatsapp-mcp"
LABEL_NEW="com.bdombro.whatsapp-client"
UID_NUM="$(id -u)"
OLD_DATA_DIR="$HOME/.local/share/whatsapp-mcp"
NEW_DATA_DIR="$HOME/.local/share/whatsapp-client"
PLIST_OLD="$HOME/Library/LaunchAgents/${LABEL_OLD}.plist"
PLIST_NEW="$HOME/Library/LaunchAgents/${LABEL_NEW}.plist"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/client/dist/whatsapp-client"

echo "=== 1. Checking and stopping old daemon (${LABEL_OLD}) ==="
if launchctl list | grep -q "${LABEL_OLD}"; then
    echo "Booting out ${LABEL_OLD}..."
    launchctl bootout "gui/${UID_NUM}/${LABEL_OLD}" || true
fi

echo "Waiting for any old daemon processes to exit..."
for i in {1..30}; do
    if ! pgrep -f "whatsapp-mcp daemon" >/dev/null 2>&1; then
        echo "Old daemon process has stopped."
        break
    fi
    sleep 0.5
done

if pgrep -f "whatsapp-mcp daemon" >/dev/null 2>&1; then
    echo "ERROR: Old daemon process is still running!" >&2
    exit 1
fi

if [ ! -L "${OLD_DATA_DIR}" ] && [ -S "${OLD_DATA_DIR}/daemon.sock" ]; then
    echo "Removing leftover ${OLD_DATA_DIR}/daemon.sock..."
    rm -f "${OLD_DATA_DIR}/daemon.sock"
fi

echo "=== 2. Migrating data directory ==="
if [ -d "${OLD_DATA_DIR}" ] && [ ! -L "${OLD_DATA_DIR}" ]; then
    if [ -e "${NEW_DATA_DIR}" ]; then
        echo "ERROR: Both old and new WhatsApp data directories exist; refusing to merge or overwrite." >&2
        exit 1
    fi
    echo "Moving ${OLD_DATA_DIR} -> ${NEW_DATA_DIR}..."
    mv "${OLD_DATA_DIR}" "${NEW_DATA_DIR}"
    echo "Creating compatibility symlink ${OLD_DATA_DIR} -> ${NEW_DATA_DIR}..."
    ln -s "${NEW_DATA_DIR}" "${OLD_DATA_DIR}"
elif [ ! -d "${NEW_DATA_DIR}" ]; then
    mkdir -p "${NEW_DATA_DIR}"
fi

echo "=== 3. Using workspace binary ==="
if [ ! -x "${BIN}" ]; then
    echo "ERROR: Build the WhatsApp client first; missing executable ${BIN}" >&2
    exit 1
fi
echo "✓ Using ${BIN}"

echo "=== 4. Writing LaunchAgent plist ==="
mkdir -p "$HOME/Library/LaunchAgents"
cat >"${PLIST_NEW}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${LABEL_NEW}</string>
	<key>ProgramArguments</key>
	<array>
		<string>${BIN}</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>${NEW_DATA_DIR}/daemon.log</string>
	<key>StandardErrorPath</key>
	<string>${NEW_DATA_DIR}/daemon.log</string>
</dict>
</plist>
EOF
echo "✓ Wrote ${PLIST_NEW}"

if [ -f "${PLIST_OLD}" ]; then
    echo "Removing old plist ${PLIST_OLD}..."
    rm -f "${PLIST_OLD}"
fi

echo "=== 5. Bootstrapping new daemon (${LABEL_NEW}) ==="
launchctl bootout "gui/${UID_NUM}/${LABEL_NEW}" 2>/dev/null || true
for i in {1..30}; do
    if [ ! -S "${NEW_DATA_DIR}/daemon.sock" ]; then
        break
    fi
    sleep 0.5
done
if [ -S "${NEW_DATA_DIR}/daemon.sock" ]; then
    echo "ERROR: Existing WhatsApp client did not stop; refusing to start another daemon." >&2
    exit 1
fi
sleep 1
launchctl bootstrap "gui/${UID_NUM}" "${PLIST_NEW}"
echo "✓ Bootstrapped ${LABEL_NEW}"

echo "Waiting for daemon to initialize..."
sleep 2

echo "=== 6. Verifying status ==="
"${BIN}" status
