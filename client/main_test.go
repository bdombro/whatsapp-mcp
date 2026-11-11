package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-client/whatsapp"
)

// seedDataDir writes a minimal messages.db the way the daemon would.
func seedDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	store, err := whatsapp.NewMessageStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ts := time.Date(2026, 11, 5, 14, 3, 0, 0, time.UTC)
	if err := store.StoreChat("15550000000@s.whatsapp.net", "Alice Example", ts); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreMessage("A1", "15550000000@s.whatsapp.net", "15550000000", "hello there", ts, false, "", "", "", nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Verifies reset refuses to run without --yes.
func TestCLI_resetRequiresYes(t *testing.T) {
	cmd := newResetCmd(t.TempDir())
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
}

// Verifies the root command wires every client subcommand.
func TestRootCmd_subcommands(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"daemon", "start", "stop", "restart", "status", "login", "reset", "completion"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("missing subcommand %s", name)
		}
	}
}

// Verifies status reports session and message counts as text and JSON.
func TestStatus_textAndJSON(t *testing.T) {
	dir := seedDataDir(t)
	snap := buildStatus(dir)
	if snap.Session != "no" || snap.DataDir != dir || !strings.HasSuffix(snap.Log, logFileName) {
		t.Fatalf("snapshot: %+v", snap)
	}
	var out bytes.Buffer
	if err := writeStatus(&out, snap, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 across 1 chats") {
		t.Fatalf("text: %s", out.String())
	}
	out.Reset()
	if err := writeStatus(&out, snap, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"session": "no"`) {
		t.Fatalf("json: %s", out.String())
	}
}

// Verifies the daemon loop only runs the client while logged in and stops on cancel.
func TestDaemonLoop(t *testing.T) {
	old := daemonRetryInterval
	daemonRetryInterval = time.Millisecond
	defer func() { daemonRetryInterval = old }()

	ctx, cancel := context.WithCancel(context.Background())
	var checks, runs int32
	loggedIn := func(string) bool { return atomic.AddInt32(&checks, 1)%2 == 0 }
	run := func(context.Context) error {
		if atomic.AddInt32(&runs, 1) >= 3 {
			cancel()
		}
		return errors.New("disconnected")
	}
	done := make(chan struct{})
	go func() {
		daemonLoop(ctx, "dir", loggedIn, run)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon loop did not stop")
	}
	if runs != 3 || checks < 6 {
		t.Fatalf("runs=%d checks=%d", runs, checks)
	}
}

// Verifies tildeHome shortens paths under the home directory only.
func TestTildeHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := tildeHome(filepath.Join(home, "x")); got != "~/x" {
		t.Fatalf("got %q", got)
	}
	if got := tildeHome(home); got != "~" {
		t.Fatalf("got %q", got)
	}
	if got := tildeHome("/tmp/x"); got != "/tmp/x" {
		t.Fatalf("got %q", got)
	}
}
