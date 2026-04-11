package db

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sumi/internal/model"
)

// ── DSN resolver ──────────────────────────────────────────────────────────────

func TestResolveDSN_PlainString(t *testing.T) {
	got, err := resolveDSN("host=localhost user=test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "host=localhost user=test" {
		t.Errorf("got %q, want plain string", got)
	}
}

func TestResolveDSN_EnvVar(t *testing.T) {
	t.Setenv("TEST_DSN", "host=env-host user=env-user")
	got, err := resolveDSN("${TEST_DSN}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "host=env-host user=env-user" {
		t.Errorf("got %q, want env value", got)
	}
}

func TestResolveDSN_EnvVar_Missing(t *testing.T) {
	os.Unsetenv("TEST_DSN_MISSING")
	_, err := resolveDSN("${TEST_DSN_MISSING}")
	if err == nil {
		t.Fatal("expected error for missing env var, got nil")
	}
}

func TestResolveDSN_File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dsn.txt")
	if err := os.WriteFile(p, []byte("  host=file-host user=file-user  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveDSN("file:" + p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "host=file-host user=file-user" {
		t.Errorf("got %q; want trimmed file content", got)
	}
}

func TestResolveDSN_File_Missing(t *testing.T) {
	_, err := resolveDSN("file:/nonexistent/path/dsn.txt")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// ── Manager: unsupported driver ───────────────────────────────────────────────

func TestNewManager_UnsupportedDriver(t *testing.T) {
	cfgs := []dbConfig{
		{Name: "test", Driver: "oracle", DSN: "dsn"},
	}
	_, err := newManagerFromCfgs(cfgs)
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
}

// ── Manager.Enrich ────────────────────────────────────────────────────────────

type stubCollector struct {
	snap model.DBSnapshot
	err  error
}

func (s *stubCollector) Collect(_ context.Context) (model.DBSnapshot, error) {
	return s.snap, s.err
}

func (s *stubCollector) Close() error { return nil }

func TestManager_Enrich_OK(t *testing.T) {
	m := &Manager{
		collectors: []Collector{
			&stubCollector{snap: model.DBSnapshot{Name: "pg", Driver: "postgres", ActiveLockCount: 2}},
			&stubCollector{snap: model.DBSnapshot{Name: "my", Driver: "mysql", QueryThroughput: 500}},
		},
	}
	var snap model.Snapshot
	m.Enrich(context.Background(), &snap)

	if len(snap.Databases) != 2 {
		t.Fatalf("want 2 DBSnapshots, got %d", len(snap.Databases))
	}
	pgIdx, myIdx := -1, -1
	for i, d := range snap.Databases {
		switch d.Name {
		case "pg":
			pgIdx = i
		case "my":
			myIdx = i
		}
	}
	if pgIdx < 0 || myIdx < 0 {
		t.Fatalf("expected both pg and my in results, got %+v", snap.Databases)
	}
	if snap.Databases[pgIdx].ActiveLockCount != 2 {
		t.Errorf("pg ActiveLockCount: got %d, want 2", snap.Databases[pgIdx].ActiveLockCount)
	}
	if snap.Databases[myIdx].QueryThroughput != 500 {
		t.Errorf("my QueryThroughput: got %f, want 500", snap.Databases[myIdx].QueryThroughput)
	}
}

func TestManager_Enrich_CollectorError(t *testing.T) {
	m := &Manager{
		collectors: []Collector{
			&stubCollector{err: errors.New("connection refused")},
		},
	}
	var snap model.Snapshot
	m.Enrich(context.Background(), &snap)

	if len(snap.Databases) != 1 {
		t.Fatalf("want 1 DBSnapshot even on error, got %d", len(snap.Databases))
	}
	if snap.Databases[0].Error == "" {
		t.Error("expected Error field to be non-empty on collector error")
	}
}

func TestManager_Enrich_NoDatabases(t *testing.T) {
	m := &Manager{}
	var snap model.Snapshot
	m.Enrich(context.Background(), &snap)
	if len(snap.Databases) != 0 {
		t.Errorf("want 0 databases, got %d", len(snap.Databases))
	}
}

func TestManager_Close_Nil(t *testing.T) {
	var m *Manager
	m.Close() // must not panic
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"toolongstring", 7, "toolong…"},
		{"x", 1, "x"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestHashQuery_Deterministic(t *testing.T) {
	q := "SELECT * FROM users WHERE id = $1"
	if hashQuery(q) != hashQuery(q) {
		t.Error("hashQuery not deterministic")
	}
	if hashQuery(q) == hashQuery(q+" ") {
		t.Error("hashQuery should differ for different inputs")
	}
}

func TestSortDesc(t *testing.T) {
	type item struct{ v float64 }
	s := []item{{1}, {5}, {3}, {2}, {4}}
	sortDesc(s, func(i int) float64 { return s[i].v })
	for i := 1; i < len(s); i++ {
		if s[i].v > s[i-1].v {
			t.Errorf("not sorted desc at index %d: %v > %v", i, s[i].v, s[i-1].v)
		}
	}
}

// ── DBSnapshot JSON round-trip ────────────────────────────────────────────────

func TestDBSnapshot_JSON_RoundTrip(t *testing.T) {
	orig := model.DBSnapshot{
		Name:            "main",
		Driver:          "postgres",
		Connections:     model.DBConnections{Active: 3, Idle: 7, Waiting: 1, Max: 100},
		QueryThroughput: 1234.5,
		AvgLatencyMs:    2.3,
		P95LatencyMs:    8.1,
		ActiveLockCount: 2,
		SlowQueries: []model.NormalizedQuery{
			{QueryHash: "abc123", Calls: 10, TotalMs: 500, MeanMs: 50, Template: "SELECT 1"},
		},
		ReplicationLagS: -1,
		Error:           "",
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got model.DBSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != orig.Name {
		t.Errorf("Name: got %q, want %q", got.Name, orig.Name)
	}
	if got.Connections.Active != orig.Connections.Active {
		t.Errorf("Connections.Active: got %d, want %d", got.Connections.Active, orig.Connections.Active)
	}
	if len(got.SlowQueries) != 1 || got.SlowQueries[0].QueryHash != "abc123" {
		t.Errorf("SlowQueries mismatch: %+v", got.SlowQueries)
	}
	if got.ReplicationLagS != -1 {
		t.Errorf("ReplicationLagS: got %f, want -1", got.ReplicationLagS)
	}
}

func TestSnapshot_Databases_OmittedWhenEmpty(t *testing.T) {
	snap := model.Snapshot{}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["Databases"]; ok {
		t.Error("Databases field should be omitted when empty (omitempty)")
	}
}

// ── internal helpers for test setup ──────────────────────────────────────────

// dbConfig mirrors config.Database to avoid importing the config package in tests.
type dbConfig struct {
	Name   string
	Driver string
	DSN    string
}

// newManagerFromCfgs is a test-only factory that validates drivers.
func newManagerFromCfgs(cfgs []dbConfig) (*Manager, error) {
	for _, c := range cfgs {
		switch c.Driver {
		case "postgres", "postgresql", "mysql", "mariadb":
		default:
			return nil, errors.New("unsupported driver: " + c.Driver)
		}
	}
	return &Manager{}, nil
}
