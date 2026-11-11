package main

import (
	"fmt"
	"os"
	"strings"

	"whatsapp-client/core"
	"whatsapp-client/whatsapp"

	"github.com/spf13/cobra"
)

// newRootCmd builds the Cobra command tree for the CLI.
func newRootCmd() *cobra.Command {
	dataDir := core.DataDir()
	root := &cobra.Command{
		Use:   "whatsapp-client",
		Short: "WhatsApp sync daemon and pairing CLI",
		Long: `whatsapp-client keeps a local copy of your WhatsApp chats in sync via whatsmeow into SQLite in ` + dataDir + `.
It provides a local REST API over daemon.sock for sending messages and downloading media.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newDaemonCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newStatusCmd(dataDir),
		newLoginCmd(dataDir),
		newResetCmd(dataDir),
		newCompletionCmd(root),
	)
	return root
}

// dispatchCLI executes the Cobra CLI, printing errors to stderr and exiting non-zero on failure.
func dispatchCLI(args []string) {
	initLogger()
	root := newRootCmd()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if !isUsageError(err) {
			core.ReportErr("cli:"+root.CalledAs(), err)
		}
		core.Flush()
		os.Exit(1)
	}
}

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon",
		Aliases: []string{"core"},
		Short:   "Run the sync daemon in the foreground (used by the LaunchAgent)",
		Args:    cobra.NoArgs,
		Run:     func(*cobra.Command, []string) { runDaemon() },
	}
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon LaunchAgent",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { runStart() },
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon LaunchAgent",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { runStop() },
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon LaunchAgent",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { runRestart() },
	}
}

func newStatusCmd(dataDir string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon, session and data status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeStatus(cmd.OutOrStdout(), buildStatus(dataDir), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newLoginCmd(dataDir string) *cobra.Command {
	var relogin bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Pair with WhatsApp by scanning a QR code (stop the daemon first)",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { whatsapp.RunLogin(dataDir, relogin) },
	}
	cmd.Flags().BoolVar(&relogin, "relogin", false, "Delete the existing session and messages, then pair again")
	return cmd
}

func newResetCmd(dataDir string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Delete the WhatsApp session and all synced messages (stop the daemon first)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if !yes {
				return fmt.Errorf("this deletes whatsapp.db and messages.db in %s; re-run with --yes to confirm", dataDir)
			}
			whatsapp.RunReset(dataDir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm deletion")
	return cmd
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate shell autocompletion script",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "bash",
		Short: "Generate autocompletion script for bash",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return root.GenBashCompletion(cmd.OutOrStdout()) },
	}, &cobra.Command{
		Use:   "zsh",
		Short: "Generate autocompletion script for zsh",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return root.GenZshCompletion(cmd.OutOrStdout()) },
	})
	return cmd
}

// isUsageError reports whether err is Cobra rejecting the command line, which is user input and not worth reporting.
func isUsageError(err error) bool {
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "required flag", "accepts ", "invalid argument", "flag needs", "this deletes "} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}
