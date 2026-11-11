package main

import (
	"log/slog"
	"os"
)

// initLogger configures the process-wide slog default to a text handler on stderr at INFO level.
// Called once from the CLI/daemon entrypoints so all slog.Default() calls share consistent output.
func initLogger() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
}
