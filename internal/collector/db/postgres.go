package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sync"

	_ "github.com/lib/pq" // register "postgres" driver

	"sumi/internal/model"
)

// pgCollector collects metrics from a PostgreSQL instance.
type pgCollector struct {
	name string
	db   *sql.DB

	mu       sync.Mutex
	prevRows map[string]pgStmtRow // queryid → last-tick row for delta computation
}

type pgStmtRow struct {
	calls     int64
	totalExec float64 // total_exec_time in ms
}

func newPostgres(name, dsn string) (*pgCollector, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(5 * 60 * 1e9) // 5 min in nanoseconds
	return &pgCollector{name: name, db: db}, nil
}

func (c *pgCollector) Close() error { return c.db.Close() }

func (c *pgCollector) Collect(ctx context.Context) (model.DBSnapshot, error) {
	snap := model.DBSnapshot{Name: c.name, Driver: "postgres", ReplicationLagS: -1}

	if err := c.collectConnections(ctx, &snap); err != nil {
		return snap, fmt.Errorf("pg connections: %w", err)
	}
	// pg_stat_statements is optional — skip on error.
	_ = c.collectStatements(ctx, &snap)
	_ = c.collectLocks(ctx, &snap)
	_ = c.collectReplicationLag(ctx, &snap)

	return snap, nil
}

// collectConnections reads pg_stat_activity and pg_settings.
func (c *pgCollector) collectConnections(ctx context.Context, snap *model.DBSnapshot) error {
	// Max connections from pg_settings.
	var maxConns int
	err := c.db.QueryRowContext(ctx,
		`SELECT setting::int FROM pg_settings WHERE name = 'max_connections'`).
		Scan(&maxConns)
	if err != nil {
		return fmt.Errorf("max_connections: %w", err)
	}
	snap.Connections.Max = maxConns

	// Active / idle / waiting counts from pg_stat_activity.
	rows, err := c.db.QueryContext(ctx,
		`SELECT state, count(*)
		   FROM pg_stat_activity
		  WHERE pid <> pg_backend_pid()
		  GROUP BY state`)
	if err != nil {
		return fmt.Errorf("pg_stat_activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var state sql.NullString
		var cnt int
		if err := rows.Scan(&state, &cnt); err != nil {
			continue
		}
		switch state.String {
		case "active":
			snap.Connections.Active = cnt
		case "idle":
			snap.Connections.Idle = cnt
		case "idle in transaction":
			// grouped into waiting for simplicity
			snap.Connections.Waiting += cnt
		}
	}
	return rows.Err()
}

// collectStatements reads pg_stat_statements delta since last tick.
// Requires the pg_stat_statements extension; silently returns nil if absent.
func (c *pgCollector) collectStatements(ctx context.Context, snap *model.DBSnapshot) error {
	rows, err := c.db.QueryContext(ctx,
		`SELECT queryid::text, query, calls, total_exec_time, mean_exec_time
		   FROM pg_stat_statements
		  WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		  ORDER BY total_exec_time DESC
		  LIMIT 100`)
	if err != nil {
		// Extension not installed or permission denied — not a hard error.
		return nil
	}
	defer rows.Close()

	type stmtRow struct {
		queryID   string
		query     string
		calls     int64
		totalExec float64
		meanExec  float64
	}
	var stmts []stmtRow
	for rows.Next() {
		var s stmtRow
		if err := rows.Scan(&s.queryID, &s.query, &s.calls, &s.totalExec, &s.meanExec); err != nil {
			continue
		}
		stmts = append(stmts, s)
	}
	if err := rows.Err(); err != nil {
		return nil
	}

	c.mu.Lock()
	prev := c.prevRows
	// Build new map and compute deltas.
	newPrev := make(map[string]pgStmtRow, len(stmts))
	type deltaRow struct {
		queryID    string
		query      string
		deltaCalls int64
		deltaTotal float64
		meanExec   float64
	}
	var deltas []deltaRow
	for _, s := range stmts {
		newPrev[s.queryID] = pgStmtRow{calls: s.calls, totalExec: s.totalExec}
		if prev == nil {
			continue
		}
		p, ok := prev[s.queryID]
		if !ok {
			continue
		}
		dc := s.calls - p.calls
		dt := s.totalExec - p.totalExec
		if dc <= 0 {
			continue
		}
		deltas = append(deltas, deltaRow{
			queryID:    s.queryID,
			query:      s.query,
			deltaCalls: dc,
			deltaTotal: dt,
			meanExec:   s.meanExec,
		})
	}
	c.prevRows = newPrev
	c.mu.Unlock()

	// Sort by deltaTotal desc, take top 5.
	sortDesc(deltas, func(i int) float64 { return deltas[i].deltaTotal })
	top := deltas
	if len(top) > 5 {
		top = top[:5]
	}
	for _, d := range top {
		snap.SlowQueries = append(snap.SlowQueries, model.NormalizedQuery{
			QueryHash: hashQuery(d.query),
			Calls:     d.deltaCalls,
			TotalMs:   d.deltaTotal,
			MeanMs:    d.meanExec,
			Template:  truncate(d.query, 200),
		})
	}

	// Aggregate throughput and average latency from deltas.
	for _, d := range deltas {
		snap.QueryThroughput += float64(d.deltaCalls)
		snap.AvgLatencyMs += d.deltaTotal
	}
	return nil
}

// collectLocks counts ungranted lock requests.
func (c *pgCollector) collectLocks(ctx context.Context, snap *model.DBSnapshot) error {
	return c.db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_locks WHERE NOT granted`).
		Scan(&snap.ActiveLockCount)
}

// collectReplicationLag reads replica lag if this instance is a standby.
func (c *pgCollector) collectReplicationLag(ctx context.Context, snap *model.DBSnapshot) error {
	var lag sql.NullFloat64
	err := c.db.QueryRowContext(ctx,
		`SELECT extract(epoch FROM now() - pg_last_xact_replay_timestamp())`).
		Scan(&lag)
	if err != nil {
		return nil // primary or pg_last_xact_replay_timestamp unavailable
	}
	if lag.Valid && lag.Float64 >= 0 {
		snap.ReplicationLagS = lag.Float64
	}
	return nil
}

// ---- helpers ----

func hashQuery(q string) string {
	sum := sha256.Sum256([]byte(q))
	return fmt.Sprintf("%x", sum[:8])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// sortDesc sorts slice s in descending order by key.
func sortDesc[T any](s []T, key func(int) float64) {
	n := len(s)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && key(j) > key(j-1); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
