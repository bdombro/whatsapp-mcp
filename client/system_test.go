package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

// Verifies launchctl output with a PID extracts the expected daemon process ID.
func TestParseLaunchctlPID(t *testing.T) {
	output := `{
	"Label" = "com.bdombro.whatsapp-client";
	"PID" = 51454;
}`
	if got := parseLaunchctlPID(output); got != 51454 {
		t.Fatalf("parseLaunchctlPID() = %d, want 51454", got)
	}
}

// Verifies launchctl output without a PID falls back to zero instead of misparsing.
func TestParseLaunchctlPID_missing(t *testing.T) {
	if got := parseLaunchctlPID(`{"Label" = "com.bdombro.whatsapp-client";}`); got != 0 {
		t.Fatalf("parseLaunchctlPID() = %d, want 0", got)
	}
}

// Verifies ps RSS kilobytes convert into bytes for daemon memory reporting.
func TestParseProcessRSSBytes(t *testing.T) {
	if got := parseProcessRSSBytes("896384\n"); got != 917897216 {
		t.Fatalf("parseProcessRSSBytes() = %d, want 917897216", got)
	}
}

// Verifies invalid ps RSS output safely returns zero instead of propagating garbage values.
func TestParseProcessRSSBytes_invalid(t *testing.T) {
	if got := parseProcessRSSBytes("not-a-number"); got != 0 {
		t.Fatalf("parseProcessRSSBytes() = %d, want 0", got)
	}
}

// Verifies coreDaemonPID returns zero when the plist is missing or launchctl
// fails, and returns the parsed PID when the LaunchAgent is running.
func TestCoreDaemonPID(t *testing.T) {
	t.Run("plist missing", func(t *testing.T) {
		oldStatPath := coreDaemonStatPath
		coreDaemonStatPath = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
		defer func() { coreDaemonStatPath = oldStatPath }()

		if got := coreDaemonPID(); got != 0 {
			t.Fatalf("coreDaemonPID() = %d, want 0", got)
		}
	})

	t.Run("launchctl parse", func(t *testing.T) {
		oldStatPath := coreDaemonStatPath
		oldLaunchctlList := coreDaemonLaunchctlList
		coreDaemonStatPath = func(string) (os.FileInfo, error) { return testFileInfo{name: "plist"}, nil }
		coreDaemonLaunchctlList = func(string) ([]byte, error) { return []byte(`{"PID" = 789;}`), nil }
		defer func() {
			coreDaemonStatPath = oldStatPath
			coreDaemonLaunchctlList = oldLaunchctlList
		}()

		if got := coreDaemonPID(); got != 789 {
			t.Fatalf("coreDaemonPID() = %d, want 789", got)
		}
	})

	t.Run("launchctl error", func(t *testing.T) {
		oldStatPath := coreDaemonStatPath
		oldLaunchctlList := coreDaemonLaunchctlList
		coreDaemonStatPath = func(string) (os.FileInfo, error) { return testFileInfo{name: "plist"}, nil }
		coreDaemonLaunchctlList = func(string) ([]byte, error) { return nil, errors.New("launchctl failed") }
		defer func() {
			coreDaemonStatPath = oldStatPath
			coreDaemonLaunchctlList = oldLaunchctlList
		}()

		if got := coreDaemonPID(); got != 0 {
			t.Fatalf("coreDaemonPID() = %d, want 0", got)
		}
	})
}

// testFileInfo supplies minimal os.FileInfo behavior so daemon PID tests can
// stub plist existence without touching the real filesystem.
type testFileInfo struct{ name string }

// Name returns the fake file name for daemon PID tests.
func (f testFileInfo) Name() string { return f.name }

// Size returns zero because daemon PID tests only care that stat succeeds.
func (f testFileInfo) Size() int64 { return 0 }

// Mode returns a regular-file mode because daemon PID tests only care that the
// file exists.
func (f testFileInfo) Mode() os.FileMode { return 0 }

// ModTime returns the zero time because daemon PID tests do not inspect timestamps.
func (f testFileInfo) ModTime() (t time.Time) { return t }

// IsDir reports false because daemon PID tests stub a plist file rather than a directory.
func (f testFileInfo) IsDir() bool { return false }

// Sys returns nil because daemon PID tests do not use platform-specific stat data.
func (f testFileInfo) Sys() interface{} { return nil }
