package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func runMcp() {
	sources, err := LoadSources()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load data sources: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		for _, src := range sources {
			src.Close()
		}
	}()

	s := server.NewMCPServer(
		"mcp-bridge",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	for _, src := range sources {
		src.RegisterTools(s)
	}

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

// ---------- Shared tool helpers ----------

func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func intArg(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

func boolArg(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}
