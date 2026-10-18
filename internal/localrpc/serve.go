// Package localrpc owns the bounded local NDJSON transport. It has no storage or policy.
package localrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

type Handler func(context.Context, []byte) (any, error)
type Server struct{ listener *net.UnixListener }

func Listen(path string) (*Server, error) {
	if path == "" {
		return nil, fmt.Errorf("socket path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("socket path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return &Server{listener: listener}, nil
}
func (s *Server) Close() error { return s.listener.Close() }
func (s *Server) Serve(ctx context.Context, handler Handler) error {
	listener := s.listener
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers, watcher sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	slots := make(chan struct{}, 16)
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		<-ctx.Done()
		listener.Close()
		mu.Lock()
		active := make([]net.Conn, 0, len(connections))
		for conn := range connections {
			active = append(active, conn)
		}
		mu.Unlock()
		for _, conn := range active {
			conn.Close()
		}
	}()
	defer func() { cancel(); watcher.Wait(); workers.Wait() }()
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		conn, err := listener.AcceptUnix()
		if err != nil {
			<-slots
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		mu.Lock()
		connections[conn] = true
		mu.Unlock()
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock(); <-slots }()
			serveConnection(ctx, conn, handler)
		}()
	}
}
func serveConnection(ctx context.Context, conn net.Conn, handler Handler) {
	scanner := bufio.NewScanner(conn)
	// Large-journal put/get already allow up to 1 GiB of records. The framing
	// bound matches the CLI's 2 GiB input bound without an unbounded reader.
	scanner.Buffer(make([]byte, 64<<10), 2<<30)
	encoder := json.NewEncoder(conn)
	for {
		if ctx.Err() != nil {
			return
		}
		conn.SetDeadline(time.Now().Add(5 * time.Minute))
		if !scanner.Scan() {
			return
		}
		// A backfill can exceed the normal request window. Keep its response
		// writable long enough to report a bounded embedding timeout.
		conn.SetWriteDeadline(time.Now().Add(31 * time.Minute))
		result, err := handler(ctx, scanner.Bytes())
		if err != nil {
			result = map[string]string{"error": err.Error()}
		}
		if encoder.Encode(result) != nil {
			return
		}
	}
}
