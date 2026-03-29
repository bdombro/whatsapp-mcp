package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const plistName = "com.whatsapp-cli.core"

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistName+".plist")
}

func installedBinPath() string {
	return "/usr/local/bin/whatsapp-cli"
}

func requireDaemonInstalled() string {
	plist := plistPath()
	if _, err := os.Stat(plist); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error: core daemon not installed. Run 'whatsapp-cli install-daemon' first.")
		os.Exit(1)
	}
	return plist
}

func isLoggedIn() bool {
	waDB := filepath.Join(dataDir(), "whatsapp.db")
	if _, err := os.Stat(waDB); err != nil {
		return false
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=3000", waDB))
	if err != nil {
		return false
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var jid string
	err = db.QueryRowContext(ctx, "SELECT jid FROM whatsmeow_device WHERE jid != '' LIMIT 1").Scan(&jid)
	return err == nil && jid != ""
}

func requireLogin() {
	if !isLoggedIn() {
		fmt.Fprintln(os.Stderr, "Not logged in to WhatsApp. Run 'whatsapp-cli login' first.")
		os.Exit(1)
	}
}

func runLogin(args []string) {
	relogin := false
	for _, arg := range args {
		if arg == "--relogin" || arg == "-relogin" {
			relogin = true
		}
	}

	if isLoggedIn() && !relogin {
		fmt.Println("Already logged in.")
		runInfo()
		return
	}

	if relogin {
		fmt.Println("Re-logging in: clearing existing session...")
		// Stop daemon if running so it doesn't interfere
		plist := plistPath()
		if _, err := os.Stat(plist); err == nil {
			exec.Command("launchctl", "unload", plist).Run()
		}
		// Remove only the whatsapp session DB (not messages.db)
		os.Remove(filepath.Join(dataDir(), "whatsapp.db"))
		// Also clear messages.db so fresh history sync replaces stale data
		os.Remove(filepath.Join(dataDir(), "messages.db"))
	}

	dir := dataDir()
	os.MkdirAll(dir, 0755)

	logger := waLog.Stdout("Login", "INFO", true)
	dbLog := waLog.Stdout("Database", "INFO", true)

	container, err := sqlstore.New(context.Background(), "sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", filepath.Join(dir, "whatsapp.db")), dbLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			deviceStore = container.NewDevice()
		} else {
			fmt.Fprintf(os.Stderr, "Error getting device: %v\n", err)
			os.Exit(1)
		}
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client.Store.ID != nil {
		fmt.Println("Already logged in.")
		client.Disconnect()
		return
	}

	// Initialize message store to capture history sync during login
	messageStore, err := NewMessageStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not open message store: %v\n", err)
	}

	fullyConnected := make(chan struct{}, 1)
	var historySyncCount int
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Connected:
			select {
			case fullyConnected <- struct{}{}:
			default:
			}
		case *events.HistorySync:
			if messageStore != nil {
				handleHistorySync(client, messageStore, v, logger)
				historySyncCount++
				fmt.Printf("Received history sync event #%d (%d conversations)\n", historySyncCount, len(v.Data.Conversations))
			}
		}
	})

	qrChan, _ := client.GetQRChannel(context.Background())
	if err := client.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting: %v\n", err)
		os.Exit(1)
	}

	paired := make(chan bool, 1)
	for evt := range qrChan {
		if evt.Event == "code" {
			fmt.Println("\nScan this QR code with your WhatsApp app:")
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
		} else if evt.Event == "success" {
			paired <- true
			break
		}
	}

	select {
	case <-paired:
		fmt.Println("\nPaired! Waiting for connection to stabilize...")
	case <-time.After(3 * time.Minute):
		fmt.Fprintln(os.Stderr, "Timeout waiting for QR code scan")
		os.Exit(1)
	}

	select {
	case <-fullyConnected:
		fmt.Println("Successfully logged in!")
		// Wait for history sync events to arrive and be processed
		fmt.Println("Waiting for initial history sync (up to 60 seconds)...")
		time.Sleep(60 * time.Second)
		if historySyncCount > 0 {
			fmt.Printf("Captured %d history sync event(s) during login.\n", historySyncCount)
		}
	case <-time.After(30 * time.Second):
		fmt.Println("Paired but connection didn't fully establish. Try running 'whatsapp-cli core' to verify.")
	}

	if messageStore != nil {
		messageStore.Close()
	}
	client.Disconnect()

	// Restart daemon if --relogin stopped it
	if relogin {
		plist := plistPath()
		if _, err := os.Stat(plist); err == nil {
			fmt.Println("Restarting core daemon...")
			exec.Command("launchctl", "load", plist).Run()
		}
	}
}

func runInstallDaemon() {
	requireLogin()
	dDir := dataDir()
	binPath := installedBinPath()
	logPath := filepath.Join(dDir, "core.log")
	plist := plistPath()
	os.MkdirAll(filepath.Dir(plist), 0755)
	os.MkdirAll(dDir, 0755)

	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>core</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, plistName, binPath, logPath, logPath)

	if err := os.WriteFile(plist, []byte(plistContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing plist: %v\n", err)
		os.Exit(1)
	}

	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading launch agent: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Installed and started core daemon: %s\n", plist)
	fmt.Printf("Logs: %s\n", logPath)
}

func runStart() {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Started core daemon")
}

func runStop() {
	plist := requireDaemonInstalled()
	if err := exec.Command("launchctl", "unload", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Stopped core daemon")
}

func runRestart() {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error restarting daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Restarted core daemon")
}

func removeDaemon() {
	plist := plistPath()
	exec.Command("launchctl", "unload", plist).Run()
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error removing plist: %v\n", err)
	} else if err == nil {
		fmt.Println("Removed core daemon")
	}
}

func runReset() {
	removeDaemon()

	dDir := dataDir()
	if _, err := os.Stat(dDir); os.IsNotExist(err) {
		fmt.Println("Nothing to reset (data directory does not exist)")
		return
	}
	if err := os.RemoveAll(dDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing data directory %s: %v\n", dDir, err)
		os.Exit(1)
	}
	fmt.Printf("Removed all data: %s\n", dDir)
}

func runUninstall() {
	runReset()

	// Remove shell completions from .zshrc
	home, _ := os.UserHomeDir()
	zshrc := filepath.Join(home, ".zshrc")
	compLine := `eval "$(whatsapp-cli completions zsh)"`
	if data, err := os.ReadFile(zshrc); err == nil {
		original := string(data)
		lines := strings.Split(original, "\n")
		var filtered []string
		for _, l := range lines {
			if !strings.Contains(l, "whatsapp-cli") || !strings.Contains(l, "completions") {
				filtered = append(filtered, l)
			}
		}
		result := strings.Join(filtered, "\n")
		if result != original {
			os.WriteFile(zshrc, []byte(result), 0644)
			fmt.Println("Removed shell completions from ~/.zshrc")
		}
	}
	_ = compLine

	if err := exec.Command("sudo", "rm", "-f", "/usr/local/bin/whatsapp-cli").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not remove /usr/local/bin/whatsapp-cli (try running with sudo)\n")
	} else {
		fmt.Println("Removed /usr/local/bin/whatsapp-cli")
	}
}

func runInfo() {
	dDir := dataDir()

	fmt.Println("┌──────────────────────────────────────────┐")
	fmt.Println("│           whatsapp-cli info              │")
	fmt.Println("└──────────────────────────────────────────┘")
	fmt.Println()

	fmt.Println("📁 Data")
	fmt.Printf("   Directory:  %s\n", dDir)
	if info, err := os.Stat(dDir); err == nil && info.IsDir() {
		fmt.Println("   Status:     initialized")
	} else {
		fmt.Println("   Status:     not initialized (run 'whatsapp-cli login')")
	}
	fmt.Println()

	fmt.Println("👤 Account")
	waDB := filepath.Join(dDir, "whatsapp.db")
	if _, err := os.Stat(waDB); err == nil {
		db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=3000", waDB))
		if err == nil {
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var jid string
			err = db.QueryRowContext(ctx, "SELECT jid FROM whatsmeow_device WHERE jid != '' LIMIT 1").Scan(&jid)
			if err == nil && jid != "" {
				fmt.Printf("   Logged in:  %s\n", jid)
			} else {
				fmt.Println("   Logged in:  no")
			}
		} else {
			fmt.Println("   Logged in:  unable to read session db")
		}
	} else {
		fmt.Println("   Logged in:  no session (run 'whatsapp-cli login')")
	}

	msgDB := filepath.Join(dDir, "messages.db")
	if _, err := os.Stat(msgDB); err == nil {
		db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=3000", msgDB))
		if err == nil {
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var chatCount, msgCount int
			db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats").Scan(&chatCount)
			db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&msgCount)
			fmt.Printf("   Messages:   %d across %d chats\n", msgCount, chatCount)
		}
	} else {
		fmt.Println("   Messages:   no database yet")
	}
	fmt.Println()

	fmt.Println("⚙️  Core Daemon")
	plist := plistPath()
	if _, err := os.Stat(plist); err == nil {
		ctxLC, cancelLC := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelLC()
		out, err := exec.CommandContext(ctxLC, "launchctl", "list", plistName).Output()
		if err == nil && len(out) > 0 {
			fmt.Println("   Status:     running")
		} else {
			fmt.Println("   Status:     installed (not running)")
		}
		fmt.Printf("   Plist:      %s\n", plist)
		fmt.Printf("   Logs:       %s\n", filepath.Join(dDir, "core.log"))
	} else {
		fmt.Println("   Status:     not installed")
	}
}

var commands = []string{
	"core",
	"login",
	"mcp",
	"install-daemon",
	"uninstall",
	"start",
	"stop",
	"restart",
	"reset",
	"info",
	"completions",
}

func runCompletions(shell string) {
	switch shell {
	case "bash":
		printBashCompletions()
	case "zsh":
		printZshCompletions()
	default:
		fmt.Fprintf(os.Stderr, "Unsupported shell: %s (supported: bash, zsh)\n", shell)
		os.Exit(1)
	}
}

func printBashCompletions() {
	fmt.Print(`_whatsapp_cli() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local cmd="${COMP_WORDS[1]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "` + strings.Join(commands, " ") + `" -- "$cur") )
        return
    fi

    case "$cmd" in
        completions)
            COMPREPLY=( $(compgen -W "bash zsh" -- "$cur") )
            ;;
    esac
}
complete -o nospace -F _whatsapp_cli whatsapp-cli
`)
}

func printZshCompletions() {
	fmt.Print(`_whatsapp_cli() {
    local -a cmds comp_args

    cmds=(
        'core:Start the WhatsApp connection and REST API'
        'login:Log in to WhatsApp (scan QR code)'
        'mcp:Start the MCP server (stdio transport)'
        'install-daemon:Install core daemon (macOS LaunchAgent)'
        'uninstall:Remove daemon, data, and binaries'
        'start:Start the core daemon'
        'stop:Stop the core daemon'
        'restart:Restart the core daemon'
        'reset:Uninstall daemon and wipe all data'
        'info:Show install status and data locations'
        'completions:Print shell completions (bash or zsh)'
    )
    comp_args=(
        'bash:Bash completions'
        'zsh:Zsh completions'
    )

    if (( CURRENT == 2 )); then
        _describe -t commands 'command' cmds
    else
        case "${words[2]}" in
            completions)
                _describe -t shells 'shell' comp_args
                ;;
        esac
    fi
}

if (( ! $+functions[compdef] )); then
    autoload -Uz compinit && compinit -C
fi
compdef _whatsapp_cli whatsapp-cli
`)
}
