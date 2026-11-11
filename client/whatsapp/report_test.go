package whatsapp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

func TestClassifyEvent(t *testing.T) {
	for _, evt := range []interface{}{
		&events.LoggedOut{Reason: events.ConnectFailureLoggedOut},
		&events.StreamReplaced{},
		&events.ConnectFailure{Reason: events.ConnectFailureMainDeviceGone},
		&events.TemporaryBan{Code: 1},
		&events.ClientOutdated{},
		&events.StreamError{Code: "515"},
	} {
		key, msg, ok := classifyEvent(evt)
		if !ok || !strings.HasPrefix(key, "conn:") || msg == "" {
			t.Errorf("%T: key=%q msg=%q ok=%v", evt, key, msg, ok)
		}
	}
	for _, evt := range []interface{}{&events.Connected{}, &events.Disconnected{}, &events.Message{}, "x"} {
		if _, _, ok := classifyEvent(evt); ok {
			t.Errorf("%T should not classify", evt)
		}
	}
}

func TestReportSessionEventNoPanic(_ *testing.T) {
	reportSessionEvent(&events.LoggedOut{})
	reportSessionEvent(&events.LoggedOut{})
	reportSessionEvent(&events.Connected{})
}

func TestWatchDisconnected(t *testing.T) {
	var connected atomic.Bool
	var reports atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watchDisconnected(ctx, connected.Load, 30*time.Millisecond, 5*time.Millisecond, func(time.Duration) { reports.Add(1) })
		close(done)
	}()
	time.Sleep(120 * time.Millisecond)
	if n := reports.Load(); n != 1 {
		t.Fatalf("want exactly 1 report while disconnected, got %d", n)
	}
	connected.Store(true)
	time.Sleep(30 * time.Millisecond)
	connected.Store(false)
	time.Sleep(120 * time.Millisecond)
	if n := reports.Load(); n != 2 {
		t.Fatalf("want a second report after recovery and relapse, got %d", n)
	}
	cancel()
	<-done
	reportLongDisconnect(20 * time.Minute)
}
