package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"whatsapp-client/core"
	"whatsapp-client/whatsapp"
)

// plistName is the LaunchAgent label for the sync daemon.
const plistName = "com.bdombro.whatsapp-client"

// logFileName is the daemon log inside the data dir.
const logFileName = "daemon.log"

// daemonRetryInterval is how long the daemon waits before reconnecting or re-checking for a session.
var daemonRetryInterval = 30 * time.Second

// plistPath returns the LaunchAgent plist path so daemon commands target the installed service file.
func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistName+".plist")
}

// requireDaemonInstalled returns the installed plist path, or exits after printing install guidance.
func requireDaemonInstalled() string {
	plist := plistPath()
	if _, err := os.Stat(plist); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error: daemon not installed. Expected LaunchAgent:", plist)
		os.Exit(1)
	}
	return plist
}

// runStart loads the LaunchAgent so macOS starts the daemon.
func runStart() {
	plist := requireDaemonInstalled()
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		slog.Error("error starting daemon", "err", err)
		os.Exit(1)
	}
	fmt.Println("Started daemon")
}

// runStop unloads the LaunchAgent so macOS stops the daemon.
func runStop() {
	plist := requireDaemonInstalled()
	if err := exec.Command("launchctl", "unload", plist).Run(); err != nil {
		slog.Error("error stopping daemon", "err", err)
		os.Exit(1)
	}
	fmt.Println("Stopped daemon")
}

// runRestart reloads the LaunchAgent so macOS restarts the daemon.
func runRestart() {
	runStart()
}

// trimLogFile keeps the daemon log bounded so a long-lived LaunchAgent does not grow it forever.
func trimLogFile(dataDir string) {
	path := filepath.Join(dataDir, logFileName)
	if err := core.TrimLogFilePath(path, core.LogTrimThresholdBytes, core.LogKeepTailBytes); err != nil {
		slog.Warn("could not trim log file", "path", path, "err", err)
		core.ReportErr("daemon:trim_log", err)
	}
}

// runDaemon is the LaunchAgent entrypoint: it keeps the WhatsApp client connected while a paired session exists.
func runDaemon() {
	dir := core.DataDir()
	trimLogFile(dir)
	slog.Info("starting whatsapp-client daemon", "data_dir", dir)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if !whatsapp.IsLoggedIn(dir) {
		slog.Info("no WhatsApp session yet; daemon.sock will accept browser pairing or run 'whatsapp-client login'", "data_dir", dir)
	}
	daemonLoop(ctx, dir, func(string) bool { return true }, func(ctx context.Context) error {
		src := whatsapp.NewSource(dir)
		defer src.Close()
		return src.StartCore(ctx)
	})
	slog.Info("daemon stopped")
}

// daemonLoop runs the client while logged in, waiting between attempts, until ctx is cancelled.
func daemonLoop(ctx context.Context, dir string, loggedIn func(string) bool, run func(context.Context) error) {
	warned := false
	for ctx.Err() == nil {
		if loggedIn(dir) {
			warned = false
			if err := run(ctx); err != nil {
				slog.Error("whatsapp client error", "err", err)
				core.ReportErr("daemon:client", err)
			}
		} else if !warned {
			slog.Info("no WhatsApp session; run 'whatsapp-client login'", "data_dir", dir)
			warned = true
		}
		select {
		case <-ctx.Done():
		case <-time.After(daemonRetryInterval):
		}
	}
}
