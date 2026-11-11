package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

func TestResolveDSN(t *testing.T) {
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	if got := ResolveDSN(env(nil)); got != "" {
		t.Fatalf("unset: got %q", got)
	}
	if got := ResolveDSN(env(map[string]string{"BUGSINK_DSN": ""})); got != "" {
		t.Fatalf("empty should disable: got %q", got)
	}
	if got := ResolveDSN(env(map[string]string{"BUGSINK_DSN": " https://k@h/9 "})); got != "https://k@h/9" {
		t.Fatalf("override: got %q", got)
	}
	if got := ResolveDSN(env(map[string]string{"BUGSINK_DSN": " \t "})); got != "" {
		t.Fatal("whitespace should disable reporting")
	}
}

func TestDeduper(t *testing.T) {
	now := time.Unix(1000, 0)
	d := &Deduper{seen: map[string]time.Time{}, now: func() time.Time { return now }}
	if !d.Allow("conn:a", time.Hour) {
		t.Fatal("first should pass")
	}
	if d.Allow("conn:a", time.Hour) {
		t.Fatal("repeat should be silenced")
	}
	if !d.Allow("store:b", time.Hour) {
		t.Fatal("other key should pass")
	}
	now = now.Add(2 * time.Hour)
	if !d.Allow("conn:a", time.Hour) {
		t.Fatal("should pass after window")
	}
	d.ClearPrefix("conn:")
	if !d.Allow("conn:a", time.Hour) {
		t.Fatal("cleared key should pass")
	}
	if d.Allow("store:b", 24*time.Hour) {
		t.Fatal("uncleared key stays silenced")
	}
}

func TestScrubText(t *testing.T) {
	got := ScrubText("send to 15550000000@s.whatsapp.net or +1 5550000001 and 155500000000000000@g.us ok 42")
	for _, bad := range []string{"15550000000", "5550000001", "155500000000000000"} {
		if strings.Contains(got, bad) {
			t.Fatalf("not scrubbed: %q", got)
		}
	}
	if !strings.Contains(got, "ok 42") {
		t.Fatalf("over-scrubbed: %q", got)
	}
}

func TestScrubEvent(t *testing.T) {
	e := &sentry.Event{Message: "x 15550000000@s.whatsapp.net"}
	e.Exception = []sentry.Exception{{Value: "call 15550000000"}}
	e.Request = &sentry.Request{URL: "u"}
	out := scrubEvent(e, nil)
	if strings.Contains(out.Message, "1555") || strings.Contains(out.Exception[0].Value, "1555") || out.Request != nil {
		t.Fatalf("event not scrubbed: %+v", out)
	}
}

func TestUnexpected(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil":       {nil, false},
		"plain":     {errors.New("boom"), true},
		"user":      {MarkUser(errors.New("bad input")), false},
		"wrapped":   {fmt.Errorf("ctx: %w", MarkUser(errors.New("bad"))), false},
		"not found": {fmt.Errorf("chat x not found: %w", sql.ErrNoRows), false},
		"canceled":  {context.Canceled, false},
		"deadline":  {fmt.Errorf("x: %w", context.DeadlineExceeded), false},
	}
	for name, c := range cases {
		if got := Unexpected(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
	if MarkUser(nil) != nil {
		t.Error("MarkUser(nil) should be nil")
	}
}

func TestRecoverReportRepanics(t *testing.T) {
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("expected re-panic, got %v", r)
		}
	}()
	func() {
		defer RecoverReport()
		panic("boom")
	}()
}

func TestInitBugsinkDisabled(t *testing.T) {
	t.Setenv("BUGSINK_DSN", "")
	if InitBugsink("test", "cli") {
		t.Fatal("empty BUGSINK_DSN should disable")
	}
}
