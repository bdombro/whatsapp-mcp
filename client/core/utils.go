package core

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // register SQLite driver for database/sql
)

// IsNetworkAvailable reports whether any non-loopback network interface is up
// and has a routable (non-link-local) IP address. This is a pure OS syscall —
// zero network traffic, sub-millisecond — so it can be called freely.
func IsNetworkAvailable() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				return true
			}
		}
	}
	return false
}

// DefaultReset removes a list of files (by relative path under dataDir).
// Missing files are silently skipped. Returns the first non-missing error.
func DefaultReset(dataDir string, files []string) error {
	var firstErr error
	for _, f := range files {
		path := filepath.Join(dataDir, f)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("could not remove file", "path", path, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// RunPollLoop runs fn immediately then on every interval tick until ctx is done.
// Errors from fn are printed but do not stop the loop.
// When the network is unavailable, the tick is skipped.
func RunPollLoop(ctx context.Context, interval time.Duration, fn func(context.Context) error) error {
	if IsNetworkAvailable() {
		if err := fn(ctx); err != nil {
			slog.Error("sync error", "err", err)
			ReportErr("sync", err)
		}
	} else {
		slog.Info("network unavailable, skipping initial sync")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !IsNetworkAvailable() {
				slog.Info("network unavailable, skipping sync")
				continue
			}
			if err := fn(ctx); err != nil {
				slog.Error("sync error", "err", err)
				ReportErr("sync", err)
			}
		}
	}
}

// OpenDB opens a SQLite database at filepath.Join(dataDir, filename) with
// shared pragmas (WAL mode, 30-second busy timeout, foreign keys on).
func OpenDB(dataDir, filename string) (*sql.DB, error) {
	path := filepath.Join(dataDir, filename)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(on)&_pragma=busy_timeout(30000)", path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=30000")
	return db, nil
}
