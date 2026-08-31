package mcp

import (
	"io"
	"testing"

	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
)

func TestNewBuildsServeNamespace(t *testing.T) {
	command := New("test-version", func(io.Writer) memoryservice.Config {
		return memoryservice.Config{DataDir: t.TempDir()}
	})
	if command.Use != "mcp" {
		t.Fatalf("use = %q, want mcp", command.Use)
	}
	serve, _, err := command.Find([]string{"serve"})
	if err != nil || serve == command || serve.Use != "serve" {
		t.Fatalf("serve command is not registered: %v", err)
	}
	if !serve.SilenceUsage {
		t.Fatal("serve command must suppress CLI usage on protocol failures")
	}
}
