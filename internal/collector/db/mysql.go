package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	_ "github.com/go-sql-driver/mysql" // register "mysql" driver

	"sumi/internal/model"
)

// mysqlCollector collects metrics from a MySQL / MariaDB instance.
type mysqlCollector struct {
	name string
	db   *sql.DB

	mu           sync.Mutex
	prevQueries  uint64 // Queries counter snapshot for delta throughput
	prevStmtRows map[string]mysqlStmtRow
}

type mysqlStmtRow struct {
	countStar    uint64
	sumTimerWait uint64 // in picoseconds
}

func newMySQL(name, dsn string) (*mysqlCollector, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(5 * 60 * 1e9)
	return &mysqlCollector{name: name, db: db}, nil
}

func (c *mysqlCollector) Close() error { return c.db.Close() }

func (c *mysqlCollector) Collect(ctx context.Context) (model.DBSnapshot, error) {
	snap := model.DBSnapshot{Name: c.name, Driver: "mysql", ReplicationLagS: -1}

	if err := c.collectConnections(ctx, &snap); err != nil {
		return snap, fmt.Errorf("mysql connections: %w", err)
	}
	_ = c.collectThroughput(ctx, &snap)
	_ = c.collectStatements(ctx, &snap)
	_ = c.collectLocks(ctx, &snap)
	_ = c.collectReplicationLag(ctx, &snap)

	return snap, nil
}

// collectConnections reads SHOW GLOBAL STATUS + SHOW VARIABLES.
func (c *mysqlCollector) collectConnections(ctx context.Context, snap *model.DBSnapshot) error {
	statusRows, err := c.db.QueryContext(ctx,
		`SELECT variable_name, variable_value
		   FROM information_schema.global_status
		  WHERE variable_name IN ('THREADS_CONNECTED','THREADS_RUNNING')`)
	if err != nil {
		return fmt.Errorf("global_status: %w", err)
	}
	defer statusRows.Close()
	for statusRows.Next() {
		var name, val string
		if err := statusRows.Scan(&name, &val); err != nil {
			continue
		}
		var n int
		fmt.Sscan(val, &n)
		switch name {
		case "THREADS_CONNECTED":
			snap.Connections.Active = n
		case "THREADS_RUNNING":
			// Running < Connected; we approximate Active = Running, Idle = Connected-Running.
			snap.Connections.Active = n
			// Will be corrected below after both values are read.
		}
	}
	if err := statusRows.Err(); err != nil {
		return err
	}

	var maxConns int
	if err := c.db.QueryRowContext(ctx,
		`SELECT variable_value FROM information_schema.global_variables WHERE variable_name = 'MAX_CONNECTIONS'`).
		Scan(&maxConns); err == nil {
		snap.Connections.Max = maxConns
	}
	if snap.Connections.Max > 0 {
		snap.Connections.Idle = snap.Connections.Max - snap.Connections.Active
		if snap.Connections.Idle < 0 {
			snap.Connections.Idle = 0
		}
	}
	return nil
}

// collectThroughput reads the Queries counter delta.
func (c *mysqlCollector) collectThroughput(ctx context.Context, snap *model.DBSnapshot) error {
	var val string
	err := c.db.QueryRowContext(ctx,
		`SELECT variable_value FROM information_schema.global_status WHERE variable_name = 'QUERIES'`).
		Scan(&val)
	if err != nil {
		return nil
	}
	var queries uint64
	fmt.Sscan(val, &queries)

	c.mu.Lock()
	prev := c.prevQueries
	c.prevQueries = queries
	c.mu.Unlock()

	if prev > 0 && queries >= prev {
		snap.QueryThroughput = float64(queries - prev)
	}
	return nil
}

// collectStatements reads performance_schema digest table delta.
// Silently skipped if performance_schema is disabled or inaccessible.
func (c *mysqlCollector) collectStatements(ctx context.Context, snap *model.DBSnapshot) error {
	rows, err := c.db.QueryContext(ctx,
		`SELECT DIGEST, DIGEST_TEXT, COUNT_STAR, SUM_TIMER_WAIT, AVG_TIMER_WAIT
		   FROM performance_schema.events_statements_summary_by_digest
		  WHERE SCHEMA_NAME IS NOT NULL
		  ORDER BY SUM_TIMER_WAIT DESC
		  LIMIT 100`)
	if err != nil {
		return nil // performance_schema disabled
	}
	defer rows.Close()

	type row struct {
		digest     string
		digestText string
		countStar  uint64
		sumTimer   uint64 // picoseconds
		avgTimer   uint64 // picoseconds
	}
	var stmts []row
	for rows.Next() {
		var s row
		var digestText sql.NullString
		if err := rows.Scan(&s.digest, &digestText, &s.countStar, &s.sumTimer, &s.avgTimer); err != nil {
			continue
		}
		s.digestText = digestText.String
		stmts = append(stmts, s)
	}
	if rows.Err() != nil {
		return nil
	}

	c.mu.Lock()
	prev := c.prevStmtRows
	newPrev := make(map[string]mysqlStmtRow, len(stmts))
	type delta struct {
		digest     string
		digestText string
		dCount     uint64
		dTimer     uint64
		avgTimer   uint64
	}
	var deltas []delta
	for _, s := range stmts {
		newPrev[s.digest] = mysqlStmtRow{countStar: s.countStar, sumTimerWait: s.sumTimer}
		if prev == nil {
			continue
		}
		p, ok := prev[s.digest]
		if !ok {
			continue
		}
		if s.countStar <= p.countStar {
			continue
		}
		deltas = append(deltas, delta{
			digest:     s.digest,
			digestText: s.digestText,
			dCount:     s.countStar - p.countStar,
			dTimer:     s.sumTimer - p.sumTimerWait,
			avgTimer:   s.avgTimer,
		})
	}
	c.prevStmtRows = newPrev
	c.mu.Unlock()

	// Sort by dTimer desc.
	sortDesc(deltas, func(i int) float64 { return float64(deltas[i].dTimer) })
	top := deltas
	if len(top) > 5 {
		top = top[:5]
	}
	for _, d := range top {
		const ps2ms = 1e-9 // picoseconds to milliseconds
		totalMs := float64(d.dTimer) * ps2ms
		meanMs := float64(d.avgTimer) * ps2ms
		snap.SlowQueries = append(snap.SlowQueries, model.NormalizedQuery{
			QueryHash: d.digest[:8],
			Calls:     int64(d.dCount),
			TotalMs:   totalMs,
			MeanMs:    meanMs,
			Template:  truncate(d.digestText, 200),
		})
	}
	return nil
}

// collectLocks counts sessions in LOCK WAIT state from InnoDB.
func (c *mysqlCollector) collectLocks(ctx context.Context, snap *model.DBSnapshot) error {
	err := c.db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.INNODB_TRX WHERE trx_state = 'LOCK WAIT'`).
		Scan(&snap.ActiveLockCount)
	if err != nil {
		return nil // non-fatal
	}
	return nil
}

// collectReplicationLag reads Seconds_Behind_Source from SHOW REPLICA STATUS.
func (c *mysqlCollector) collectReplicationLag(ctx context.Context, snap *model.DBSnapshot) error {
	rows, err := c.db.QueryContext(ctx, `SHOW REPLICA STATUS`)
	if err != nil {
		// Try legacy syntax for older MySQL versions.
		rows, err = c.db.QueryContext(ctx, `SHOW SLAVE STATUS`)
		if err != nil {
			return nil // not a replica or no permission
		}
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !rows.Next() {
		return nil
	}
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil
	}
	// Find the Seconds_Behind_Source / Seconds_Behind_Master column.
	for i, col := range cols {
		if col == "Seconds_Behind_Source" || col == "Seconds_Behind_Master" {
			if v, ok := vals[i].(int64); ok {
				snap.ReplicationLagS = float64(v)
			}
			break
		}
	}
	return nil
}
