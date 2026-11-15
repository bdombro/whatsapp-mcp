package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func saveAndRestoreStdio(t *testing.T) {
	t.Helper()
	stdoutFd := int(os.Stdout.Fd())
	stderrFd := int(os.Stderr.Fd())
	savedOut, err := unix.Dup(stdoutFd)
	if err != nil {
		t.Fatalf("dup stdout: %v", err)
	}
	savedErr, err := unix.Dup(stderrFd)
	if err != nil {
		_ = unix.Close(savedOut)
		t.Fatalf("dup stderr: %v", err)
	}
	t.Cleanup(func() {
		_ = unix.Dup2(savedOut, stdoutFd)
		_ = unix.Dup2(savedErr, stderrFd)
		_ = unix.Close(savedOut)
		_ = unix.Close(savedErr)
	})
}

func pollUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

// Verifies SetupLogFile redirects stdout/stderr and rotates at a tiny threshold keeping only .1.
func TestLogFile_rotatesAtThreshold(t *testing.T) {
	saveAndRestoreStdio(t)
	logPath := filepath.Join(t.TempDir(), "logs", "client.log")

	ctx, cancel := context.WithCancel(context.Background())
	wait, err := SetupLogFile(ctx, logPath, 100, 10*time.Millisecond)
	if err != nil {
		cancel()
		t.Fatalf("SetupLogFile: %v", err)
	}
	// Register wait before cancel so LIFO runs cancel() first, stopping the goroutine before cleanup.
	t.Cleanup(wait)
	t.Cleanup(cancel)

	fmt.Fprintln(os.Stdout, strings.Repeat("a", 70))
	fmt.Fprintln(os.Stderr, strings.Repeat("b", 70))

	pollUntil(t, 2*time.Second, func() bool {
		_, err1 := os.Stat(logPath + ".1")
		info, err0 := os.Stat(logPath)
		return err1 == nil && err0 == nil && info.Size() == 0
	})

	fmt.Fprintln(os.Stdout, strings.Repeat("c", 120))
	pollUntil(t, 2*time.Second, func() bool {
		data, err := os.ReadFile(logPath + ".1")
		return err == nil && strings.Contains(string(data), strings.Repeat("c", 120))
	})

	if _, err := os.Stat(logPath + ".2"); !os.IsNotExist(err) {
		t.Fatalf("expected no .2 backup file, got err=%v", err)
	}

	fmt.Fprintln(os.Stderr, "after-second-rotation")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read active log: %v", err)
	}
	if !strings.Contains(string(data), "after-second-rotation") {
		t.Fatalf("expected new write in %s, got %q", logPath, string(data))
	}
}

// Verifies SetupLogFile rotates a pre-existing oversized log file on startup.
func TestLogFile_rotatesExistingOversizedOnStartup(t *testing.T) {
	saveAndRestoreStdio(t)
	logPath := filepath.Join(t.TempDir(), "client.log")
	if err := os.WriteFile(logPath, []byte(strings.Repeat("x", 200)), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	wait, err := SetupLogFile(ctx, logPath, 100, 50*time.Millisecond)
	if err != nil {
		cancel()
		t.Fatalf("SetupLogFile: %v", err)
	}
	t.Cleanup(wait)
	t.Cleanup(cancel)

	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("expected %s.1 to exist: %v", logPath, err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat %s: %v", logPath, err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected fresh empty %s, got size %d", logPath, info.Size())
	}
}

// Verifies SetupLogFile returns errors cleanly on invalid arguments or unwritable path.
func TestLogFile_invalidInputs(t *testing.T) {
	ctx := context.Background()
	if _, err := SetupLogFile(ctx, "", 100, time.Millisecond); err == nil {
		t.Fatal("expected error for empty path")
	}
	if _, err := SetupLogFile(ctx, filepath.Join(t.TempDir(), "a.log"), 0, time.Millisecond); err == nil {
		t.Fatal("expected error for non-positive maxBytes")
	}
	if _, err := SetupLogFile(ctx, filepath.Join(t.TempDir(), "a.log"), 100, 0); err == nil {
		t.Fatal("expected error for non-positive checkInterval")
	}

	// File as parent directory triggers MkdirAll error.
	blockingFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetupLogFile(ctx, filepath.Join(blockingFile, "client.log"), 100, time.Millisecond); err == nil {
		t.Fatal("expected error when parent path is a file")
	}
}
