package whatsapp

import (
	"context"
	"fmt"
	"time"

	"whatsapp-client/core"

	"go.mau.fi/whatsmeow/types/events"
)

// DisconnectReportAfter is how long the client may stay disconnected before it is reported.
const DisconnectReportAfter = 15 * time.Minute

// classifyEvent maps a whatsmeow event that signals a lost or unhealthy session to a report key and message; ok is false for other events.
func classifyEvent(evt interface{}) (key, msg string, ok bool) {
	switch v := evt.(type) {
	case *events.LoggedOut:
		return "conn:logged_out", fmt.Sprintf("WhatsApp session logged out or device removed (reason %d); re-pair with 'whatsapp-client login'", int(v.Reason)), true
	case *events.StreamReplaced:
		return "conn:stream_replaced", "WhatsApp stream replaced: another client is using this session", true
	case *events.ConnectFailure:
		return fmt.Sprintf("conn:connect_failure:%d", int(v.Reason)), fmt.Sprintf("WhatsApp connect failure (code %d)", int(v.Reason)), true
	case *events.TemporaryBan:
		return "conn:temporary_ban", fmt.Sprintf("WhatsApp temporary ban (code %d)", int(v.Code)), true
	case *events.ClientOutdated:
		return "conn:client_outdated", "WhatsApp rejected the client as outdated; update whatsmeow", true
	case *events.StreamError:
		return "conn:stream_error:" + v.Code, "WhatsApp stream error (code " + v.Code + ")", true
	}
	return "", "", false
}

// reportSessionEvent sends a deduplicated report for session-level events and clears connection state once connected.
func reportSessionEvent(evt interface{}) {
	if _, ok := evt.(*events.Connected); ok {
		core.ClearState("conn:")
		return
	}
	if key, msg, ok := classifyEvent(evt); ok {
		core.ReportState(key, msg)
	}
}

// watchDisconnected calls report once each time the client has been disconnected for at least after, checking every interval until ctx ends.
func watchDisconnected(ctx context.Context, isConnected func() bool, after, interval time.Duration, report func(time.Duration)) {
	var since time.Time
	reported := false
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if isConnected() {
			since, reported = time.Time{}, false
			continue
		}
		if since.IsZero() {
			since = time.Now()
		}
		if !reported && time.Since(since) >= after {
			reported = true
			report(time.Since(since))
		}
	}
}

// reportLongDisconnect sends the deduplicated long-disconnect report.
func reportLongDisconnect(d time.Duration) {
	core.ReportState("conn:disconnected_long", fmt.Sprintf("WhatsApp client disconnected for %s", d.Round(time.Minute)))
}
