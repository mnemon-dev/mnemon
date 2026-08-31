// Package cmd composes the single Mnemon product command.
package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/mnemon-dev/mnemon/cmd/agency"
	mcpcmd "github.com/mnemon-dev/mnemon/cmd/mcp"
	"github.com/mnemon-dev/mnemon/cmd/memory"
	"github.com/spf13/cobra"
)

var version = "dev"

// Execute runs one Mnemon command. Process exit remains the root main package's
// responsibility; a long-running command owns any graceful signal handling it
// requires so ordinary Memory commands retain the operating system defaults.
func Execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if ctx == nil || stdin == nil || stdout == nil || stderr == nil {
		return 1
	}
	root := productRoot()
	agencyRequest := false
	command, _, findErr := root.Find(args)
	if findErr == nil {
		agencyRequest = belongsToAgency(command)
		if agencyRequest {
			root.SilenceErrors = true
		}
	}
	// Cobra renders automatic usage through the command output writer. Keep
	// successful help and version output on stdout, but render Memory's usage
	// explicitly to stderr after an execution error, as the established CLI did.
	root.SilenceUsage = true
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	executed, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	if findErr == nil && !agencyRequest && !belongsToAgency(executed) &&
		!belongsToMCP(executed) && executed != nil {
		_, _ = fmt.Fprintln(stderr, executed.UsageString())
	}
	if err.Error() != "" {
		_, _ = fmt.Fprintln(stderr, err)
	}
	if code, ok := agency.ExitCode(err); ok {
		return code
	}
	if agencyRequest || belongsToAgency(executed) {
		return 2
	}
	return 1
}

func belongsToMCP(command *cobra.Command) bool {
	for current := command; current != nil; current = current.Parent() {
		if current.Name() == "mcp" {
			return true
		}
	}
	return false
}

func belongsToAgency(command *cobra.Command) bool {
	for current := command; current != nil; current = current.Parent() {
		if current.Name() == "agency" {
			return true
		}
	}
	return false
}

func productRoot() *cobra.Command {
	root := memory.New(version)
	root.Short = "Memory and durable agency for LLM agents"
	root.Long = "Mnemon gives LLM agents persistent memory and a local authority for durable, peer-to-peer work."
	root.SilenceErrors = false
	root.SilenceUsage = false
	// Memory's current command tree is process-global. Remove a prior command
	// so focused tests can construct the product root more than once without
	// changing the production command set.
	for _, child := range root.Commands() {
		if child.Name() == "agency" || child.Name() == "mcp" {
			root.RemoveCommand(child)
		}
	}
	root.AddCommand(agency.New(version), mcpcmd.New(version, memory.RuntimeServiceConfig))
	return root
}
