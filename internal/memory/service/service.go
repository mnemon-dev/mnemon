// Package service exposes Mnemon's Memory operations independently from any
// command or transport projection.
package service

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

const defaultResultLimit = 10

// Config selects the Memory store and runtime behavior used by a Service.
type Config struct {
	DataDir      string
	StoreName    string
	ReadOnly     bool
	EmbedModel   string
	Warnings     io.Writer
	AuditContent bool
}

// Service owns the application-level Memory operations shared by the CLI and
// protocol adapters. Operations are serialized because store.DB tracks its
// active transaction on the handle and remember performs a read/decide/write
// sequence that must not race another operation in the same server process.
type Service struct {
	config Config
	gate   chan struct{}
}

// New constructs a Memory service. A single Service may be reused safely by
// concurrent callers; callers waiting for the operation gate can be canceled.
func New(config Config) *Service {
	if config.DataDir == "" {
		config.DataDir = store.DefaultDataDir()
	}
	if config.Warnings == nil {
		config.Warnings = io.Discard
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Service{config: config, gate: gate}
}

func (s *Service) acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.gate:
		if err := ctx.Err(); err != nil {
			s.gate <- struct{}{}
			return nil, err
		}
		return func() { s.gate <- struct{}{} }, nil
	}
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *Service) selectedStoreName() string {
	if s.config.StoreName != "" {
		return s.config.StoreName
	}
	if name := os.Getenv("MNEMON_STORE"); name != "" {
		return name
	}
	return store.ReadActive(s.config.DataDir)
}

func (s *Service) openDB() (*store.DB, error) {
	name := s.selectedStoreName()
	if !store.ValidStoreName(name) {
		return nil, fmt.Errorf("invalid store name %q", name)
	}
	dir := store.StoreDir(s.config.DataDir, name)
	if s.config.ReadOnly {
		return store.OpenReadOnly(dir)
	}
	if err := store.MigrateIfNeeded(s.config.DataDir); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store.Open(dir)
}

func (s *Service) openWritableDB(action string) (*store.DB, error) {
	db, err := s.openDB()
	if err != nil {
		return nil, err
	}
	if db.IsReadOnly() {
		_ = db.Close()
		return nil, fmt.Errorf("%s is unavailable with --readonly: database writes are disabled", action)
	}
	return db, nil
}

func (s *Service) auditDetail(content, redacted string) string {
	if s.config.AuditContent {
		return content
	}
	return redacted
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultResultLimit, nil
	}
	if limit < 0 {
		return 0, fmt.Errorf("limit must be greater than 0")
	}
	return limit, nil
}
