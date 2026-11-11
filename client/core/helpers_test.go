package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Verifies DataDir defaults to ~/.local/share/whatsapp-client when no env is set.
func TestDataDir_default(t *testing.T) {
	ResetDataDirForTest()
	origClient := os.Getenv(ClientDataDirEnv)
	origMcp := os.Getenv(McpDataDirEnv)
	os.Unsetenv(ClientDataDirEnv)
	os.Unsetenv(McpDataDirEnv)
	defer func() {
		ResetDataDirForTest()
		if origClient != "" {
			os.Setenv(ClientDataDirEnv, origClient)
		}
		if origMcp != "" {
			os.Setenv(McpDataDirEnv, origMcp)
		}
	}()

	dir := DataDir()
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".local", "share", "whatsapp-client")
	if dir != expected {
		t.Fatalf("expected %s, got %s", expected, dir)
	}
}

// Verifies WHATSAPP_CLIENT_DATA_DIR overrides default.
func TestDataDir_clientEnv(t *testing.T) {
	ResetDataDirForTest()
	custom := t.TempDir()
	os.Setenv(ClientDataDirEnv, custom)
	defer func() {
		os.Unsetenv(ClientDataDirEnv)
		ResetDataDirForTest()
	}()

	dir := DataDir()
	if dir != custom {
		t.Fatalf("expected %s, got %s", custom, dir)
	}
}

// Verifies WHATSAPP_MCP_DATA_DIR acts as fallback.
func TestDataDir_mcpFallbackEnv(t *testing.T) {
	ResetDataDirForTest()
	custom := t.TempDir()
	os.Unsetenv(ClientDataDirEnv)
	os.Setenv(McpDataDirEnv, custom)
	defer func() {
		os.Unsetenv(McpDataDirEnv)
		ResetDataDirForTest()
	}()

	dir := DataDir()
	if dir != custom {
		t.Fatalf("expected %s, got %s", custom, dir)
	}
}
