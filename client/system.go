package main

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var coreDaemonStatPath = os.Stat
var coreDaemonLaunchctlList = func(label string) ([]byte, error) {
	return exec.Command("launchctl", "list", label).Output()
}

// daemonRSSBytes returns the daemon RSS in bytes by resolving the LaunchAgent PID and querying `ps`.
func daemonRSSBytes(label string) int64 {
	out, err := coreDaemonLaunchctlList(label)
	if err != nil {
		return 0
	}
	pid := parseLaunchctlPID(string(out))
	if pid <= 0 {
		return 0
	}
	psOut, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	return parseProcessRSSBytes(string(psOut))
}

// coreDaemonPID returns the LaunchAgent PID when the installed core daemon is
// running, or zero when the plist is absent or launchctl reports no process.
func coreDaemonPID() int {
	if _, err := coreDaemonStatPath(plistPath()); err != nil {
		return 0
	}
	out, err := coreDaemonLaunchctlList(plistName)
	if err != nil || len(out) == 0 {
		return 0
	}
	return parseLaunchctlPID(string(out))
}

// parseLaunchctlPID extracts the numeric PID from `launchctl list` output for a loaded service.
func parseLaunchctlPID(output string) int {
	re := regexp.MustCompile(`"PID"\s*=\s*(\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0
	}
	pid, _ := strconv.Atoi(matches[1])
	return pid
}

// parseProcessRSSBytes converts `ps` RSS kilobytes into bytes, or zero when parsing fails.
func parseProcessRSSBytes(output string) int64 {
	rssKB, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil || rssKB <= 0 {
		return 0
	}
	return rssKB * 1024
}
