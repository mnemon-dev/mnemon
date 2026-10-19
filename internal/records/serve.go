package records

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mnemon-dev/mnemon/internal/localrpc"
)

// Serve keeps the original records protocol and owns its indexes and workers.
func (s *Store) Serve(ctx context.Context, path string) error { return s.ServeUsing(ctx, path, nil) }

// ServeUsing adds a product dispatcher while retaining the same records runtime.
// The caller freezes its handler and keeps all dependencies alive until return.
func (s *Store) ServeUsing(ctx context.Context, path string, handler localrpc.Handler) error {
	if path == "" {
		return errors.New("records serve requires --socket")
	}
	server, err := localrpc.Listen(path)
	if err != nil {
		return err
	}
	defer server.Close()
	stop, err := s.StartRuntime(ctx)
	if err != nil {
		return err
	}
	defer stop()
	if handler == nil {
		handler = s.Dispatch
	}
	return server.Serve(ctx, handler)
}

// StartRuntime gives the store the query runtime Serve runs, for a process
// that keeps the store open without a socket: the query cache, a pool of
// read-only connections and the background lexical worker, which a put
// wakes. Start it before the store is shared and call stop (idempotent) after
// its last call, before Close. Ending ctx stops the worker only. A store runs
// one runtime at a time: a start while one runs fails and changes nothing.
func (s *Store) StartRuntime(ctx context.Context) (stop func(), err error) {
	if !s.running.CompareAndSwap(false, true) {
		return nil, errors.New("records runtime is already running")
	}
	defer func() {
		if err != nil {
			s.running.Store(false)
		}
	}()
	s.cache = newQueryCache()
	// Readers may run concurrently in WAL mode; CAS, vector writes and lexical
	// batches keep the original single writer connection and immediate tx lock.
	readers, err := sql.Open("sqlite", sqliteDSN(s.path, true))
	if err != nil {
		s.cache = nil
		return nil, err
	}
	readers.SetMaxOpenConns(4)
	readers.SetMaxIdleConns(4)
	if err := readers.PingContext(ctx); err != nil {
		readers.Close()
		s.cache = nil
		return nil, err
	}
	s.readers = readers
	ctx, cancel := context.WithCancel(ctx)
	var worker sync.WaitGroup
	worker.Add(1)
	go func() { defer worker.Done(); s.runLexicalWorker(ctx) }()
	s.wakeLexical()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			worker.Wait()
			readers.Close()
			s.readers = nil
			s.cache = nil
			s.running.Store(false)
		})
	}, nil
}

func (s *Store) Dispatch(ctx context.Context, raw []byte) (any, error) {
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

func (s *Store) dispatch(ctx context.Context, raw []byte) (any, error) { return s.Dispatch(ctx, raw) }
