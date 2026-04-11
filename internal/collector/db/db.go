// Package db provides database performance metric collectors for sumi.
// Each configured database instance is polled independently at its own interval.
// All collectors use database/sql and require the corresponding driver to be
// imported in the binary (lib/pq for PostgreSQL, go-sql-driver/mysql for MySQL).
package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"sumi/internal/config"
	"sumi/internal/model"
)

// Collector collects one snapshot from a single database instance.
type Collector interface {
	Collect(ctx context.Context) (model.DBSnapshot, error)
	Close() error
}

// Manager owns all configured DB collectors and enriches a snapshot with
// their results. The zero value is valid and performs no collection.
type Manager struct {
	collectors []Collector
}

// NewManager creates a Manager from the provided config slice.
// Collectors that cannot connect at startup are still created — they will
// report the error in DBSnapshot.Error every tick until connectivity is restored.
// An empty cfg slice returns a no-op Manager.
func NewManager(cfgs []config.Database) (*Manager, error) {
	if len(cfgs) == 0 {
		return &Manager{}, nil
	}
	cols := make([]Collector, 0, len(cfgs))
	for _, c := range cfgs {
		dsn, err := resolveDSN(c.DSN)
		if err != nil {
			return nil, fmt.Errorf("db %q: %w", c.Name, err)
		}
		var col Collector
		switch strings.ToLower(c.Driver) {
		case "postgres", "postgresql":
			col, err = newPostgres(c.Name, dsn)
		case "mysql", "mariadb":
			col, err = newMySQL(c.Name, dsn)
		default:
			return nil, fmt.Errorf("db %q: unsupported driver %q (want \"postgres\" or \"mysql\")", c.Name, c.Driver)
		}
		if err != nil {
			return nil, fmt.Errorf("db %q: %w", c.Name, err)
		}
		cols = append(cols, col)
	}
	return &Manager{collectors: cols}, nil
}

// Enrich collects from all DB collectors concurrently and appends results to
// snap.Databases. Each collector runs with an individual deadline of 10 s.
// Errors are stored in DBSnapshot.Error and do not prevent other collectors
// from running or the snapshot from being returned.
func (m *Manager) Enrich(ctx context.Context, snap *model.Snapshot) {
	if len(m.collectors) == 0 {
		return
	}

	const perDBTimeout = 10 * time.Second

	type result struct {
		idx  int
		snap model.DBSnapshot
	}
	results := make([]result, len(m.collectors))
	var wg sync.WaitGroup

	for i, col := range m.collectors {
		wg.Add(1)
		go func(i int, col Collector) {
			defer wg.Done()
			dbCtx, cancel := context.WithTimeout(ctx, perDBTimeout)
			defer cancel()
			s, err := col.Collect(dbCtx)
			if err != nil {
				s.Error = err.Error()
			}
			results[i] = result{idx: i, snap: s}
		}(i, col)
	}
	wg.Wait()

	for _, r := range results {
		snap.Databases = append(snap.Databases, r.snap)
	}
}

// Close releases resources for all collectors. Safe to call on a nil Manager.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, c := range m.collectors {
		_ = c.Close()
	}
}

// resolveDSN expands "${ENV_VAR}" and "file:/path" references.
// Plain strings are returned as-is (dev use only; document as insecure).
func resolveDSN(raw string) (string, error) {
	if strings.HasPrefix(raw, "${") && strings.HasSuffix(raw, "}") {
		name := raw[2 : len(raw)-1]
		val := os.Getenv(name)
		if val == "" {
			return "", fmt.Errorf("env var %s is not set or empty", name)
		}
		return val, nil
	}
	if strings.HasPrefix(raw, "file:") {
		path := strings.TrimPrefix(raw, "file:")
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read DSN from file %s: %w", path, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return raw, nil
}
