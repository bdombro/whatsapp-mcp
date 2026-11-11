// Package whatsapp implements the WhatsApp sync daemon, read/write service, MCP tools and CLI helpers.
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// pairedWhatsAppJID returns the stored device JID when whatsmeow has a non-empty session row, else ("", err) for missing DB, query errors, or empty JID.
func pairedWhatsAppJID(dataDir string) (string, error) {
	waDB := filepath.Join(dataDir, "whatsapp.db")
	if _, err := os.Stat(waDB); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(30000)", waDB))
	if err != nil {
		return "", err // nocov: sqlite open failures are environment-specific and not asserted in CI
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var jid string
	err = db.QueryRowContext(ctx, "SELECT jid FROM whatsmeow_device WHERE jid != '' LIMIT 1").Scan(&jid)
	if err != nil {
		return "", err
	}
	return jid, nil
}

// IsLoggedIn checks whether whatsapp.db currently contains a non-empty paired-device JID, which is the CLI's cheap auth gate.
func IsLoggedIn(dataDir string) bool {
	jid, err := pairedWhatsAppJID(dataDir)
	return err == nil && jid != ""
}

// SessionAuthDisplay returns the paired WhatsApp JID for status when logged in, or "no" when there is no usable session.
func SessionAuthDisplay(dataDir string) string {
	jid, err := pairedWhatsAppJID(dataDir)
	if err != nil || jid == "" {
		return "no"
	}
	return jid
}

// RunLogin performs the WhatsApp QR login flow.
// Pass relogin=true to force a fresh session.
func RunLogin(dataDir string, relogin bool) {

	if IsLoggedIn(dataDir) && !relogin {
		fmt.Println("Already logged in.")
		return
	}

	if relogin {
		fmt.Println("Re-logging in: clearing existing session...")
		os.Remove(filepath.Join(dataDir, "whatsapp.db"))
		os.Remove(filepath.Join(dataDir, "messages.db"))
	}

	secureDataDir(dataDir)

	logger := waLog.Stdout("Login", "INFO", true)
	dbLog := waLog.Stdout("Database", "INFO", true)

	container, err := sqlstore.New(context.Background(), "sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(on)&_pragma=busy_timeout(30000)", filepath.Join(dataDir, "whatsapp.db")), dbLog)
	if err != nil {
		slog.Error("error opening database", "err", err)
		os.Exit(1)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			deviceStore = container.NewDevice()
		} else {
			slog.Error("error getting device", "err", err)
			os.Exit(1)
		}
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client.Store.ID != nil {
		fmt.Println("Already logged in.")
		client.Disconnect()
		return
	}

	messageStore, err := NewMessageStore(dataDir)
	if err != nil {
		slog.Warn("could not open message store", "err", err)
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
		slog.Error("error connecting to WhatsApp", "err", err)
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
		fmt.Println("Waiting for initial history sync (up to 60 seconds)...")
		time.Sleep(60 * time.Second)
		if historySyncCount > 0 {
			fmt.Printf("Captured %d history sync event(s) during login.\n", historySyncCount)
		}
	case <-time.After(30 * time.Second):
		fmt.Println("Paired but connection didn't fully establish. Check the daemon log with 'whatsapp-client status'.")
	}

	if messageStore != nil {
		messageStore.Close()
	}
	client.Disconnect()

}

// RunReset removes the WhatsApp session and message databases.
func RunReset(dataDir string) {
	if err := ResetData(dataDir); err != nil {
		slog.Warn("warning during reset", "err", err)
	}
	fmt.Println("WhatsApp data reset complete.")
}
