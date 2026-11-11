// Package main is the whatsapp-mcp CLI, sync daemon and MCP server entrypoint.
package main

import (
	"os"

	"whatsapp-client/core"
)

// Build-time variables set via -ldflags
var (
	BuildTime    = "unknown"
	BuildVersion = "dev"
)

// runMode names the process role from its first argument for error reports.
func runMode(args []string) string {
	if len(args) > 0 {
		switch args[0] {
		case "daemon", "core":
			return "daemon"
		case "mcp":
			return "mcp"
		}
	}
	return "cli"
}

// main starts error reporting, then hands process args to CLI dispatch.
func main() {
	core.InitBugsink(BuildVersion, runMode(os.Args[1:]))
	defer core.Flush()
	defer core.RecoverReport()
	dispatchCLI(os.Args[1:])
}
