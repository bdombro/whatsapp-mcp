package whatsapp

import (
	"os"
	"path/filepath"
	"testing"
)

// Verifies secureDataDir creates the data dir owner-only and tightens an older install's 0755 dir and 0644 files.
func TestSecureDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"whatsapp.db", "messages.db", "daemon.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := secureDataDir(dir); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", info.Mode().Perm())
	}
	for _, name := range []string{"whatsapp.db", "messages.db", "daemon.log"} {
		if info, _ := os.Stat(filepath.Join(dir, name)); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", name, info.Mode().Perm())
		}
	}

	fresh := filepath.Join(t.TempDir(), "new", "data")
	if err := secureDataDir(fresh); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(fresh); info.Mode().Perm() != 0o700 {
		t.Fatalf("fresh dir mode = %v", info.Mode().Perm())
	}
}
