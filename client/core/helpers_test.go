package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Verifies DataDir defaults to $(brew --prefix)/var/whatsapp-client when no env is set.
func TestDataDir_default(t *testing.T) {
	t.Cleanup(ResetDataDirForTest)
	t.Setenv(ClientDataDirEnv, "")
	t.Setenv(McpDataDirEnv, "")

	customPrefix := t.TempDir()
	t.Setenv("HOMEBREW_PREFIX", customPrefix)
	ResetDataDirForTest()
	expected := filepath.Join(customPrefix, "var", "whatsapp-client")
	if dir := DataDir(); dir != expected {
		t.Fatalf("expected %s, got %s", expected, dir)
	}

	t.Setenv("HOMEBREW_PREFIX", "")
	ResetDataDirForTest()
	expected = filepath.Join(BrewPrefix(), "var", "whatsapp-client")
	if dir := DataDir(); dir != expected {
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

// Verifies WHATSAPP_CLIENT_DATA_DIR wins even when HOMEBREW_PREFIX is set.
func TestDataDir_clientEnvWinsOverHomebrewPrefix(t *testing.T) {
	t.Cleanup(ResetDataDirForTest)
	custom := t.TempDir()
	t.Setenv("HOMEBREW_PREFIX", t.TempDir())
	t.Setenv(ClientDataDirEnv, custom)
	ResetDataDirForTest()

	if dir := DataDir(); dir != custom {
		t.Fatalf("expected %s, got %s", custom, dir)
	}
}
