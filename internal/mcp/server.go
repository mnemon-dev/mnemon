// Package mcp exposes Mnemon Memory through the Model Context Protocol.
package mcp

import (
	"context"
	"fmt"
	"io"

	"github.com/mnemon-dev/mnemon/internal/memory/search"
	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverInstructions = "Mnemon provides shared, durable memory. Recall, search, and related results are truncated by default; pass full=true only when complete stored content is needed."

// Memory is the application service consumed by the MCP adapter.
type Memory interface {
	Recall(context.Context, memoryservice.RecallRequest) (memoryservice.RecallResponse, error)
	Search(context.Context, memoryservice.SearchRequest) ([]search.ScoredInsight, error)
	Remember(context.Context, memoryservice.RememberRequest) (memoryservice.RememberResult, error)
	Related(context.Context, memoryservice.RelatedRequest) ([]memoryservice.RelatedResult, error)
	Link(context.Context, memoryservice.LinkRequest) (memoryservice.LinkResult, error)
	Status(context.Context) (memoryservice.StatusResult, error)
}

// Server owns one MCP server and its Memory application service.
type Server struct {
	memory Memory
	sdk    *sdkmcp.Server
}

// New constructs a tool-only Mnemon MCP server.
func New(version string, memory Memory) *Server {
	server := &Server{memory: memory}
	server.sdk = sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "mnemon", Version: version},
		&sdkmcp.ServerOptions{Instructions: serverInstructions},
	)
	server.registerReadTools()
	server.registerWriteTools()
	return server
}

// Serve runs one MCP stdio session until the client disconnects or ctx is
// canceled. Only protocol frames are written to stdout.
func (s *Server) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil {
		return fmt.Errorf("serve MCP: context must not be nil")
	}
	if stdin == nil {
		return fmt.Errorf("serve MCP: stdin must not be nil")
	}
	if stdout == nil {
		return fmt.Errorf("serve MCP: stdout must not be nil")
	}
	transport := &sdkmcp.IOTransport{
		Reader: io.NopCloser(stdin),
		Writer: nopWriteCloser{Writer: stdout},
	}
	return s.sdk.Run(ctx, transport)
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }
