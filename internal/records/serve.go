package records

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// Serve owns the listener, at most 16 connections and all their workers. Each
// NDJSON frame is the existing request plus operation/status routing fields;
// success responses are identical to CLI output, errors are {"error": message}.
// Closing the context interrupts idle readers, SQL and embedding requests, then
// waits for every worker. It never removes a socket created by another process.
func (s *Store) Serve(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("records serve requires --socket")
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("socket path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	s.cache = newQueryCache()
	defer func() { s.cache = nil }()
	// Readers may run concurrently in WAL mode; CAS, vector writes and lexical
	// batches keep the original single writer connection and immediate tx lock.
	readers, err := sql.Open("sqlite", sqliteDSN(s.path, true))
	if err != nil {
		return err
	}
	readers.SetMaxOpenConns(4)
	readers.SetMaxIdleConns(4)
	if err := readers.PingContext(ctx); err != nil {
		readers.Close()
		return err
	}
	s.readers = readers
	defer func() { readers.Close(); s.readers = nil }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers, watcher sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	slots := make(chan struct{}, 16)
	workers.Add(1)
	go func() {
		defer workers.Done()
		s.runLexicalWorker(ctx)
	}()
	s.wakeLexical()
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
			s.serveConnection(ctx, conn)
		}()
	}
}

func (s *Store) serveConnection(ctx context.Context, conn net.Conn) {
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
		result, err := s.dispatch(ctx, scanner.Bytes())
		if err != nil {
			result = map[string]string{"error": err.Error()}
		}
		if encoder.Encode(result) != nil {
			return
		}
	}
}

func (s *Store) dispatch(ctx context.Context, raw []byte) (any, error) {
	var route struct {
		Operation string `json:"operation"`
		Status    bool   `json:"status"`
	}
	if err := json.Unmarshal(raw, &route); err != nil {
		return nil, errors.New("Invalid records request JSON")
	}
	window := 5 * time.Minute
	if route.Operation == "embed" && !route.Status {
		window = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	switch route.Operation {
	case "get":
		var req GetRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return s.Get(ctx, req)
	case "put":
		var req PutRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		rev, err := s.Put(ctx, req)
		if err == nil {
			s.wakeLexical()
		}
		return map[string]string{"revision": rev}, err
	case "query":
		var req QueryRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return s.Query(ctx, req)
	case "embed":
		var req EmbedRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return s.Embed(ctx, req, route.Status)
	default:
		return nil, errors.New("Unknown records operation")
	}
}
