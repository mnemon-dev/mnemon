package memory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mnemon-dev/mnemon/internal/localrpc"
	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	nativeservice "github.com/mnemon-dev/mnemon/internal/memory/service"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
	"github.com/spf13/cobra"
)

func init() {
	command := &cobra.Command{Use: "serve", Short: "Serve records, native Memory reads and optional memory strategy on one local socket", Args: cobra.NoArgs, RunE: runMemoryServe}
	command.Flags().String("socket", "", "Unix socket path (required)")
	command.Flags().String("memory-config", "", "Optional versioned memory-strategy JSON configuration")
	rootCmd.AddCommand(command)
	group := &cobra.Command{Use: "memory-agent", Short: "Write original text, read a Memory View, or run bounded background consolidation"}
	for _, operation := range []string{"write", "read", "consolidate"} {
		operation := operation
		child := &cobra.Command{Use: operation, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return runMemoryAgent(cmd, operation) }}
		child.Flags().String("config", "", "Memory-strategy JSON configuration (required for read/consolidate)")
		group.AddCommand(child)
	}
	rootCmd.AddCommand(group)
}

func runMemoryServe(cmd *cobra.Command, _ []string) error {
	if !cmd.Flags().Changed("data-dir") && !cmd.InheritedFlags().Changed("data-dir") {
		return errors.New("serve requires an explicit --data-dir")
	}
	name := resolveStoreName()
	if !store.ValidStoreName(name) {
		return errors.New("invalid store name")
	}
	directory := store.StoreDir(dataDir, name)
	native, err := openDB()
	if err != nil {
		return err
	}
	defer native.Close()
	var raw *records.Store
	_, statErr := os.Stat(filepath.Join(directory, "records.db"))
	if !readOnly || !os.IsNotExist(statErr) {
		raw, err = records.Open(directory, readOnly)
		if err != nil {
			return err
		}
		defer raw.Close()
	}
	adapter := nativeservice.New(native, embed.NewClientWithModel(resolveEmbedModel()))
	path, _ := cmd.Flags().GetString("memory-config")
	agent, err := openMemoryAgent(raw, directory, path)
	if err != nil {
		return err
	}
	if agent != nil {
		defer agent.Close()
	}
	socket, _ := cmd.Flags().GetString("socket")
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	handler := func(ctx context.Context, request []byte) (any, error) {
		var route struct{ Operation string }
		if json.Unmarshal(request, &route) != nil {
			return nil, errors.New("Invalid records request JSON")
		}
		if strings.HasPrefix(route.Operation, "native.") {
			ctx, stop := context.WithTimeout(ctx, 5*time.Minute)
			defer stop()
			return adapter.Dispatch(ctx, request)
		}
		if strings.HasPrefix(route.Operation, "memory.") {
			if raw == nil {
				return nil, errors.New("records database is not initialized")
			}
			ctx, stop := context.WithTimeout(ctx, 5*time.Minute)
			defer stop()
			return dispatchMemory(ctx, raw, agent, route.Operation, request)
		}
		if raw == nil {
			return nil, errors.New("records database is not initialized")
		}
		return raw.Dispatch(ctx, request)
	}
	if raw != nil {
		return raw.ServeUsing(ctx, socket, handler)
	}
	server, err := localrpc.Listen(socket)
	if err != nil {
		return err
	}
	defer server.Close()
	return server.Serve(ctx, handler)
}

func dispatchMemory(ctx context.Context, raw *records.Store, agent *memoryagent.Agent, operation string, request []byte) (any, error) {
	if readOnly && (operation == "memory.write" || operation == "memory.consolidate") {
		return nil, readOnlyWriteError(operation)
	}
	if operation == "memory.write" {
		var req memoryagent.WriteRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return memoryagent.Write(ctx, raw, req)
	}
	if agent == nil {
		return nil, errors.New("memory strategy requires --memory-config or --config")
	}
	switch operation {
	case "memory.read":
		var req memoryagent.ReadRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return agent.Read(ctx, req)
	case "memory.consolidate":
		var req memoryagent.ConsolidateRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return agent.Consolidate(ctx, req)
	default:
		return nil, errors.New("unknown memory operation")
	}
}

func runMemoryAgent(cmd *cobra.Command, operation string) error {
	if readOnly && operation != "read" {
		return readOnlyWriteError("memory-agent " + operation)
	}
	if !cmd.Flags().Changed("data-dir") && !cmd.InheritedFlags().Changed("data-dir") {
		return errors.New("memory-agent requires an explicit --data-dir")
	}
	name := resolveStoreName()
	if !store.ValidStoreName(name) {
		return errors.New("invalid store name")
	}
	raw, err := records.Open(store.StoreDir(dataDir, name), readOnly)
	if err != nil {
		return err
	}
	defer raw.Close()
	request, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), (1<<20)+1))
	if err != nil || len(request) > 1<<20 {
		return errors.New("memory request exceeds 1 MiB or cannot be read")
	}
	var agent *memoryagent.Agent
	if operation != "write" {
		path, _ := cmd.Flags().GetString("config")
		config, err := readMemoryConfig(path)
		if err != nil {
			return err
		}
		agent, err = memoryagent.Open(raw, store.StoreDir(dataDir, name), config, readOnly)
		if err != nil {
			return err
		}
		defer agent.Close()
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()
	result, err := dispatchMemory(ctx, raw, agent, "memory."+operation, request)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
}

func readMemoryConfig(path string) (memoryagent.Config, error) {
	config := memoryagent.Config{}
	if !filepath.IsAbs(path) {
		return config, errors.New("memory configuration requires an absolute file path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return config, err
	}
	if info.Size() > 1<<20 {
		return config, errors.New("memory configuration exceeds 1 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&config); err != nil {
		return config, err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return config, errors.New("memory configuration must contain one JSON value")
	}
	return config, nil
}

func openMemoryAgent(raw *records.Store, directory, path string) (*memoryagent.Agent, error) {
	if path == "" {
		return nil, nil
	}
	config, err := readMemoryConfig(path)
	if err != nil {
		return nil, err
	}
	if config.Version != 1 {
		return nil, errors.New("memory config requires version 1")
	}
	if raw == nil {
		return nil, nil
	}
	return memoryagent.Open(raw, directory, config, readOnly)
}
