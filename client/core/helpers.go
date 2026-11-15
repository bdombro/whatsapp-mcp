// Package core provides shared helpers for the whatsapp-client daemon and CLI.
package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// BrewPrefix returns the Homebrew prefix from HOMEBREW_PREFIX, `brew --prefix`, or OS/arch defaults.
func BrewPrefix() string {
	if v := strings.TrimSpace(os.Getenv("HOMEBREW_PREFIX")); v != "" {
		return v
	}
	if out, err := exec.Command("brew", "--prefix").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	if runtime.GOOS == "darwin" {
		if runtime.GOARCH == "arm64" {
			return "/opt/homebrew"
		}
		return "/usr/local"
	}
	return "/home/linuxbrew/.linuxbrew"
}

// DefaultLogPath returns the default Homebrew service log path for whatsapp-client.
func DefaultLogPath() string {
	return filepath.Join(BrewPrefix(), "var", "log", "whatsapp-client.log")
}

// DataDir returns the app data root ($(brew --prefix)/var/whatsapp-client unless overridden).
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
		dataDirValue = filepath.Join(BrewPrefix(), "var", "whatsapp-client")
	})
	return dataDirValue
}

// ResetDataDirForTest resets the memoized data dir for unit tests.
func ResetDataDirForTest() {
	dataDirOnce = sync.Once{}
	dataDirValue = ""
}
