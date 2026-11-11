package core

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

// ReportWindow is how long one reported state is silenced before the same key may report again.
const ReportWindow = 6 * time.Hour

// ResolveDSN returns BUGSINK_DSN; unset or empty disables reporting.
func ResolveDSN(lookup func(string) (string, bool)) string {
	if v, ok := lookup("BUGSINK_DSN"); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// InitBugsink starts error reporting for this process and reports whether it is enabled. mode is daemon, mcp or cli.
func InitBugsink(release, mode string) bool {
	dsn := ResolveDSN(os.LookupEnv)
	if dsn == "" {
		return false
	}
	host, _ := os.Hostname()
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Release:          release,
		Environment:      "production",
		ServerName:       host,
		SendDefaultPII:   false,
		AttachStacktrace: true,
		BeforeSend:       scrubEvent,
	})
	if err != nil {
		slog.Warn("error reporting disabled", "err", err)
		return false
	}
	sentry.ConfigureScope(func(s *sentry.Scope) { s.SetTag("mode", mode) })
	return true
}

// Flush sends any queued reports, waiting at most two seconds.
func Flush() { sentry.Flush(2 * time.Second) }

var (
	jidPattern = regexp.MustCompile(`\b\d{5,}(?::\d+)?@[a-z.]+\b`)
	numPattern = regexp.MustCompile(`\+?\d{7,}`)
)

// ScrubText replaces JIDs and phone-number-like digit runs so reports never carry contact identifiers.
func ScrubText(s string) string {
	return numPattern.ReplaceAllString(jidPattern.ReplaceAllString(s, "<jid>"), "<num>")
}

// scrubEvent removes identifiers from the event text and drops request and user data before it is sent.
func scrubEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	event.Message = ScrubText(event.Message)
	for i := range event.Exception {
		event.Exception[i].Value = ScrubText(event.Exception[i].Value)
	}
	event.Request = nil
	event.User = sentry.User{}
	event.Breadcrumbs = nil
	return event
}

// Deduper lets a key through once per window so one bad state does not flood the reports.
type Deduper struct {
	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

// NewDeduper returns an empty Deduper using the wall clock.
func NewDeduper() *Deduper {
	return &Deduper{seen: map[string]time.Time{}, now: time.Now}
}

// Allow reports whether key may be reported now, and records it when so.
func (d *Deduper) Allow(key string, window time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.now()
	if last, ok := d.seen[key]; ok && t.Sub(last) < window {
		return false
	}
	d.seen[key] = t
	return true
}

// ClearPrefix forgets every key starting with prefix so the next state change reports again.
func (d *Deduper) ClearPrefix(prefix string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.seen {
		if strings.HasPrefix(k, prefix) {
			delete(d.seen, k)
		}
	}
}

var reports = NewDeduper()

// ReportState sends a deduplicated error-level message for a bad state; key groups and silences repeats.
func ReportState(key, msg string) { reportMessage(key, msg, sentry.LevelError) }

// ReportWarn sends a deduplicated warning-level message; key groups and silences repeats.
func ReportWarn(key, msg string) { reportMessage(key, msg, sentry.LevelWarning) }

// ClearState forgets reported keys starting with prefix, e.g. when the connection recovers.
func ClearState(prefix string) { reports.ClearPrefix(prefix) }

// reportMessage captures msg once per window for key, grouped by key.
func reportMessage(key, msg string, level sentry.Level) {
	if !reports.Allow(key, ReportWindow) {
		return
	}
	sentry.WithScope(func(s *sentry.Scope) {
		s.SetLevel(level)
		s.SetFingerprint([]string{key})
		sentry.CaptureMessage(msg)
	})
}

// ReportErr sends a deduplicated report for an unexpected error; key groups and silences repeats.
func ReportErr(key string, err error) {
	if err == nil || !Unexpected(err) {
		return
	}
	// No dedupe: Bugsink groups repeats and counts them. Group by call site plus the scrubbed message,
	// so errors that differ only by a JID or number land in one issue and distinct faults stay apart.
	sig := ScrubText(err.Error())
	sentry.WithScope(func(s *sentry.Scope) {
		s.SetFingerprint([]string{key, sig})
		sentry.CaptureException(err)
	})
}

// userError marks an error caused by the caller's input rather than a fault.
type userError struct{ error }

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e userError) Unwrap() error { return e.error }

// MarkUser flags err as a user input error so it is never reported.
func MarkUser(err error) error {
	if err == nil {
		return nil
	}
	return userError{err}
}

// Unexpected reports whether err is a fault worth reporting, not user input, a missing row or a cancellation.
func Unexpected(err error) bool {
	var ue userError
	switch {
	case err == nil, errors.As(err, &ue), errors.Is(err, sql.ErrNoRows),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	}
	return true
}

// RecoverReport is deferred directly in a goroutine: it reports a panic, flushes, then re-panics so behaviour is unchanged.
func RecoverReport() {
	if r := recover(); r != nil {
		sentry.CurrentHub().Recover(r)
		Flush()
		panic(r)
	}
}
