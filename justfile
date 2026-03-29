build:
    #!/bin/bash
    cd whatsapp-cli
    go build -o whatsapp-cli.bin .

update: build
    #!/bin/bash
    set -e
    # Install whatsapp-cli binary
    sudo cp whatsapp-cli/whatsapp-cli.bin /usr/local/bin/whatsapp-cli
    sudo chmod +x /usr/local/bin/whatsapp-cli
    echo "Installed /usr/local/bin/whatsapp-cli"

    # Install whatsapp-mcp-server source to /usr/local/lib
    sudo rm -rf /usr/local/lib/whatsapp-mcp-server
    sudo cp -r whatsapp-mcp-server /usr/local/lib/whatsapp-mcp-server
    sudo ln -sf /usr/local/lib/whatsapp-mcp-server/whatsapp-mcp-server.sh /usr/local/bin/whatsapp-mcp-server
    echo "Installed /usr/local/bin/whatsapp-mcp-server -> /usr/local/lib/whatsapp-mcp-server/"

    # Restart daemon if installed
    if whatsapp-cli info 2>/dev/null | grep -q "Status:     running"; then
        whatsapp-cli restart
        echo "Restarted core daemon"
    fi

install: update
    #!/bin/bash
    set -e
    # Install whatsapp-cli binary
    sudo cp whatsapp-cli/whatsapp-cli.bin /usr/local/bin/whatsapp-cli
    sudo chmod +x /usr/local/bin/whatsapp-cli
    echo "Installed /usr/local/bin/whatsapp-cli"

    # Install whatsapp-mcp-server source to /usr/local/lib
    sudo rm -rf /usr/local/lib/whatsapp-mcp-server
    sudo cp -r whatsapp-mcp-server /usr/local/lib/whatsapp-mcp-server
    sudo ln -sf /usr/local/lib/whatsapp-mcp-server/whatsapp-mcp-server.sh /usr/local/bin/whatsapp-mcp-server
    echo "Installed /usr/local/bin/whatsapp-mcp-server -> /usr/local/lib/whatsapp-mcp-server/"

    # Install daemon and cron
    whatsapp-cli login
    whatsapp-cli install-daemon
    whatsapp-cli install-cron

    # Shell completions
    COMP_LINE='eval "$(whatsapp-cli completions zsh)"'
    if ! grep -qF "$COMP_LINE" ~/.zshrc 2>/dev/null; then
        echo "" >> ~/.zshrc
        echo "$COMP_LINE" >> ~/.zshrc
        echo "Added shell completions to ~/.zshrc (restart your terminal or run: source ~/.zshrc)"
    else
        echo "Shell completions already in ~/.zshrc"
    fi

reset:
    whatsapp-cli reset
    whatsapp-cli login
    whatsapp-cli sync --from=2020.01.01