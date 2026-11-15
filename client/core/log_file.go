package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// DefaultLogMaxBytes is the size threshold (10 MB) above which the log file rotates to .1.
	DefaultLogMaxBytes int64 = 10 * 1024 * 1024
	// DefaultLogCheckInterval is how often the daemon checks the log file size for rotation.
	DefaultLogCheckInterval = 5 * time.Second
)

// SetupLogFile redirects stdout and stderr to path and rotates path to path+".1" once its size exceeds maxBytes.
func SetupLogFile(ctx context.Context, path string, maxBytes int64, checkInterval time.Duration) (func(), error) {
	if path == "" {
		return nil, errors.New("log file path must not be empty")
	}
	if maxBytes <= 0 {
		return nil, errors.New("log max bytes must be positive")
	}
	if checkInterval <= 0 {
		return nil, errors.New("log check interval must be positive")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("rotate existing log: %w", err)
		}
	}
	if err := openAndRedirectLog(path); err != nil {
		return nil, err
	}

	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rotateLogIfNeeded(&mu, path, maxBytes)
			}
		}
	}()

	return func() { <-done }, nil
}

func openAndRedirectLog(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", path, err)
	}
	fd := int(f.Fd())
	stdoutFd := int(os.Stdout.Fd())
	stderrFd := int(os.Stderr.Fd())
	defer func() {
		if fd != stdoutFd && fd != stderrFd {
			_ = f.Close()
		}
	}()

	if err := unix.Dup2(fd, stdoutFd); err != nil {
		return fmt.Errorf("redirect stdout to %s: %w", path, err)
	}
	if err := unix.Dup2(fd, stderrFd); err != nil {
		return fmt.Errorf("redirect stderr to %s: %w", path, err)
	}
	return nil
}

func rotateLogIfNeeded(mu *sync.Mutex, path string, maxBytes int64) {
	mu.Lock()
	defer mu.Unlock()

	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return
	}
	if err := os.Rename(path, path+".1"); err != nil {
		fmt.Fprintf(os.Stderr, "log rotate rename %s: %v\n", path, err)
		return
	}
	if err := openAndRedirectLog(path); err != nil {
		fmt.Fprintf(os.Stderr, "log rotate reopen %s: %v\n", path, err)
	}
}
