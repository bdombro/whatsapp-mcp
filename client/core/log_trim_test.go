package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Verifies TrimLogFilePath delegates the final write to the injectable writer so tests can simulate IO failures.
func TestTrimLogFilePath_writeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trim.log")
	content := []byte("old-a\nold-b\nkeep-1\nkeep-2\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	restore := SetTrimLogWriteForTest(func(*os.File, []byte) error {
		return errors.New("write failed")
	})
	t.Cleanup(restore)
	if err := TrimLogFilePath(path, 20, 15); err == nil {
		t.Fatal("expected write error")
	}
}
