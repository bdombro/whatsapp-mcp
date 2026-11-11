package core

import (
	"bytes"
	"errors"
	"io"
	"os"
)

// LogTrimThresholdBytes and LogKeepTailBytes match the daemon.log rotation policy.
const (
	LogTrimThresholdBytes = 5 * 1024 * 1024
	LogKeepTailBytes      = 1 * 1024 * 1024
)

// trimLogWrite performs the final rewrite of a trimmed log file so tests can inject failures.
var trimLogWrite = func(file *os.File, tail []byte) error {
	_, err := file.Write(tail)
	return err
}

// SetTrimLogWriteForTest swaps the log trim writer and returns a restore function for tests.
func SetTrimLogWriteForTest(fn func(*os.File, []byte) error) (restore func()) {
	prev := trimLogWrite
	trimLogWrite = fn
	return func() { trimLogWrite = prev }
}

// TrimLogFilePath rewrites a large log file in place with only its newest tail so long-lived processes keep bounded disk use.
func TrimLogFilePath(path string, trimThresholdBytes, keepTailBytes int64) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() <= trimThresholdBytes || keepTailBytes <= 0 {
		return nil
	}

	start := info.Size() - keepTailBytes
	if start < 0 {
		start = 0
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	tail := make([]byte, info.Size()-start)
	n, err := file.ReadAt(tail, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	tail = tail[:n]
	if start > 0 {
		if newlineIndex := bytes.IndexByte(tail, '\n'); newlineIndex >= 0 {
			tail = tail[newlineIndex+1:]
		}
	}

	file, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer file.Close()

	return trimLogWrite(file, tail)
}
