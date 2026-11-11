package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"whatsapp-client/core"
	"whatsapp-client/whatsapp"
)

// statusSnapshot is the shape of `whatsapp-mcp status --json`.
type statusSnapshot struct {
	Version  string   `json:"version"`
	Built    string   `json:"built"`
	Daemon   string   `json:"daemon"`
	PID      int      `json:"pid,omitempty"`
	RAM      string   `json:"ram,omitempty"`
	Plist    string   `json:"plist,omitempty"`
	Log      string   `json:"log"`
	DataDir  string   `json:"data_dir"`
	Session  string   `json:"session"`
	WhatsApp []string `json:"whatsapp"`
}

// buildStatus gathers install, daemon and data state without changing anything.
func buildStatus(dir string) statusSnapshot {
	snap := statusSnapshot{
		Version: BuildVersion,
		Built:   BuildTime,
		Daemon:  "not installed",
		Log:     filepath.Join(dir, logFileName),
		DataDir: dir,
		Session: whatsapp.SessionAuthDisplay(dir),
	}
	if _, err := coreDaemonStatPath(plistPath()); err == nil {
		snap.Plist = plistPath()
		snap.Daemon = "installed (not running)"
		if pid := coreDaemonPID(); pid > 0 {
			snap.Daemon = "running"
			snap.PID = pid
			if rss := daemonRSSBytes(plistName); rss > 0 {
				snap.RAM = core.FormatSizeMB(rss) + " RSS"
			}
		}
	}
	snap.WhatsApp = whatsapp.InfoLines(dir)
	for i, line := range snap.WhatsApp {
		snap.WhatsApp[i] = strings.TrimSpace(line)
	}
	return snap
}

// writeStatus prints the status snapshot as text or JSON.
func writeStatus(w io.Writer, snap statusSnapshot, asJSON bool) error {
	if asJSON {
		data, err := json.MarshalIndent(snap, "", "  ")
		if err != nil { // nocov
			return err
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}
	fmt.Fprintf(w, "whatsapp-client %s (built %s)\n\n", snap.Version, snap.Built)
	fmt.Fprintf(w, "Daemon\n   Status:     %s\n", snap.Daemon)
	if snap.PID > 0 {
		fmt.Fprintf(w, "   PID:        %d\n", snap.PID)
	}
	if snap.RAM != "" {
		fmt.Fprintf(w, "   RAM:        %s\n", snap.RAM)
	}
	if snap.Plist != "" {
		fmt.Fprintf(w, "   Plist:      %s\n", tildeHome(snap.Plist))
	}
	fmt.Fprintf(w, "   Log:        %s\n\n", tildeHome(snap.Log))
	fmt.Fprintf(w, "Data\n   Directory:  %s\n", tildeHome(snap.DataDir))
	for _, line := range snap.WhatsApp {
		fmt.Fprintf(w, "   %s\n", line)
	}
	return nil
}

// tildeHome replaces the home directory prefix with ~ for shorter display paths.
func tildeHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}
