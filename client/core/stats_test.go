package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Verifies size formatting clamps zero and renders megabytes with one decimal place for status output.
func TestFormatSizeMB(t *testing.T) {
	if got := FormatSizeMB(0); got != "0.0 MB" {
		t.Fatalf("FormatSizeMB(0) = %q", got)
	}
	if got := FormatSizeMB(1572864); got != "1.5 MB" {
		t.Fatalf("FormatSizeMB(1572864) = %q", got)
	}
}

// Verifies grouped SQLite file sizing includes the base DB plus existing WAL and SHM sidecars.
func TestFileGroupSizeBytes(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "data.db")
	if err := os.WriteFile(base, []byte("1234"), 0o600); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := os.WriteFile(base+"-wal", []byte("123"), 0o600); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	if err := os.WriteFile(base+"-shm", []byte("12"), 0o600); err != nil {
		t.Fatalf("write shm: %v", err)
	}
	if got := FileGroupSizeBytes(base); got != 9 {
		t.Fatalf("FileGroupSizeBytes() = %d, want 9", got)
	}
}
