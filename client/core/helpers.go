// Package core provides shared helpers for the whatsapp-client daemon and CLI.
package core

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	dataDirOnce  sync.Once
	dataDirValue string
)

// DataDir env var overrides: WHATSAPP_CLIENT_DATA_DIR takes precedence, followed by WHATSAPP_MCP_DATA_DIR.
const (
	ClientDataDirEnv = "WHATSAPP_CLIENT_DATA_DIR"
	McpDataDirEnv    = "WHATSAPP_MCP_DATA_DIR"
)

// DataDir returns the app data root (~/.local/share/whatsapp-client unless overridden).
func DataDir() string {
	dataDirOnce.Do(func() {
		if v := os.Getenv(ClientDataDirEnv); v != "" {
			dataDirValue = v
			return
		}
		if v := os.Getenv(McpDataDirEnv); v != "" {
			dataDirValue = v
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dataDirValue = filepath.Join(home, ".local", "share", "whatsapp-client")
	})
	return dataDirValue
}

// ResetDataDirForTest resets the memoized data dir for unit tests.
func ResetDataDirForTest() {
	dataDirOnce = sync.Once{}
	dataDirValue = ""
}
