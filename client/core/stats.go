package core

import (
	"fmt"
	"os"
)

// FormatSizeMB formats byte counts for human-facing status output, clamping zero or negative values to `0.0 MB`.
func FormatSizeMB(bytes int64) string {
	if bytes <= 0 {
		return "0.0 MB"
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

// FileGroupSizeBytes returns the size of a SQLite database file plus common
// sidecar files like -wal and -shm when they exist.
func FileGroupSizeBytes(path string) int64 {
	paths := []string{path, path + "-wal", path + "-shm"}
	var total int64
	for _, candidate := range paths {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		total += info.Size()
	}
	return total
}
