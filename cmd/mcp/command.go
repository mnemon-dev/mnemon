// Package mcp composes Mnemon's Model Context Protocol command namespace.
package mcp

import (
	"io"

	mcpserver "github.com/mnemon-dev/mnemon/internal/mcp"
	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
	"github.com/spf13/cobra"
)

// ConfigProvider resolves the parsed product-level Memory flags at execution
// time.
type ConfigProvider func(warnings io.Writer) memoryservice.Config

// New returns the `mnemon mcp` command namespace.
func New(version string, provideConfig ConfigProvider) *cobra.Command {
	root := &cobra.Command{
		Use:   "mcp",
		Short: "Expose Mnemon memory through the Model Context Protocol",
	}
	serve := &cobra.Command{
		Use:          "serve",
		Short:        "Serve MCP over standard input and output",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config := provideConfig(cmd.ErrOrStderr())
			config.AuditContent = false
			memory := memoryservice.New(config)
			server := mcpserver.New(version, memory)
			return server.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	root.AddCommand(serve)
	return root
}
