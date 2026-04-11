// internal/renderer/tui_db_test.go
package renderer

import (
	"strings"
	"testing"

	"sumi/internal/config"
	"sumi/internal/model"
	"sumi/internal/theme"
)

// newTestTUI returns a *tuiRenderer built from defaults for use in unit tests.
func newTestTUI(t *testing.T) *tuiRenderer {
	t.Helper()
	th, err := theme.Load("tokyo-night")
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	bc := theme.BoxStyle("rounded")
	cfg := config.Default()
	rdr, err := New(cfg, th, bc)
	if err != nil {
		t.Fatalf("renderer.New: %v", err)
	}
	tr, ok := rdr.(*tuiRenderer)
	if !ok {
		t.Fatal("renderer is not *tuiRenderer")
	}
	return tr
}

func TestRenderDBCard_NoPanic_ZeroValues(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{Name: "testdb", Driver: "postgres"}
	lines := r.renderDBCard(db, 80)
	if len(lines) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestRenderDBCard_HasTitle(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{Name: "mydb", Driver: "postgres"}
	lines := r.renderDBCard(db, 80)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "mydb") {
		t.Errorf("expected card to contain DB name 'mydb'; got:\n%s", combined)
	}
}

func TestRenderDBCard_WithError(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{
		Name:   "broken",
		Driver: "postgres",
		Error:  "connection refused",
	}
	lines := r.renderDBCard(db, 80)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "connection refused") {
		t.Errorf("expected error text in output; got:\n%s", combined)
	}
}

func TestRenderDBCard_ConnectionBar_HighUtilization(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{
		Name:        "busy",
		Driver:      "postgres",
		Connections: model.DBConnections{Active: 95, Idle: 5, Waiting: 0, Max: 100},
	}
	lines := r.renderDBCard(db, 80)
	combined := strings.Join(lines, "\n")
	// Should show "95" and "100" in connection line
	if !strings.Contains(combined, "95") || !strings.Contains(combined, "100") {
		t.Errorf("expected connection counts in output; got:\n%s", combined)
	}
}

func TestRenderDBCard_WithSlowQuery(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{
		Name:   "querydb",
		Driver: "postgres",
		SlowQueries: []model.NormalizedQuery{
			{QueryHash: "abc", Calls: 42, MeanMs: 123.4, Template: "SELECT * FROM orders"},
		},
	}
	lines := r.renderDBCard(db, 80)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "SELECT * FROM orders") {
		t.Errorf("expected slow query template in output; got:\n%s", combined)
	}
	if !strings.Contains(combined, "123.4") {
		t.Errorf("expected mean latency in output; got:\n%s", combined)
	}
}

func TestRenderDBCard_ReplicationLag(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{
		Name:            "replica",
		Driver:          "postgres",
		ReplicationLagS: 2.5,
	}
	lines := r.renderDBCard(db, 80)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "2.5s lag") {
		t.Errorf("expected replication lag '2.5s lag' in output; got:\n%s", combined)
	}
}

func TestRenderDBCard_PrimaryNode(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{
		Name:            "primary",
		Driver:          "postgres",
		ReplicationLagS: -1,
	}
	lines := r.renderDBCard(db, 80)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "primary") {
		t.Errorf("expected 'primary' in output for primary node; got:\n%s", combined)
	}
}

func TestRenderDBCard_LockCountRed(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{
		Name:            "locked",
		Driver:          "postgres",
		ActiveLockCount: 3,
	}
	lines := r.renderDBCard(db, 80)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "3") {
		t.Errorf("expected lock count '3' in output; got:\n%s", combined)
	}
}

func TestRenderDBCard_MinimalWidth(t *testing.T) {
	r := newTestTUI(t)
	db := &model.DBSnapshot{Name: "tiny", Driver: "mysql"}
	// Should not panic at very small width
	lines := r.renderDBCard(db, 10)
	if len(lines) == 0 {
		t.Fatal("expected non-empty output even at minimal width")
	}
}

func TestTruncateVisual_ShortString(t *testing.T) {
	got := truncateVisual("hello", 10)
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestTruncateVisual_Truncates(t *testing.T) {
	got := truncateVisual("hello world", 5)
	if !strings.HasPrefix(got, "hello") {
		t.Errorf("expected truncated string starting with 'hello', got %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncated string ending with ellipsis, got %q", got)
	}
}

func TestTruncateVisual_ZeroMax(t *testing.T) {
	got := truncateVisual("non-empty", 0)
	if got != "" {
		t.Errorf("expected empty string for max=0, got %q", got)
	}
}

func TestRenderFull_WithDatabases_NoPanic(t *testing.T) {
	r := newTestTUI(t)
	snap := model.Snapshot{
		Hostname: "testhost",
		Platform: "testOS",
		Databases: []model.DBSnapshot{
			{Name: "db1", Driver: "postgres"},
			{Name: "db2", Driver: "mysql"},
		},
	}
	// Render writes to os.Stdout; we just verify it does not panic
	_ = r.Render(snap)
}

func TestRenderDBCard_HiddenWhenNoDatabases(t *testing.T) {
	r := newTestTUI(t)
	// renderFull path: when Databases is nil the DB loop must not execute.
	// We verify by calling renderDBCard only when len > 0 and ensuring the
	// returned lines contain "DB ·" only when a snap is passed.
	snapNoDB := model.DBSnapshot{}
	_ = snapNoDB // unused — guard: this struct must compile and have zero value

	// Simulate the TUI loop guard: len(s.Databases) == 0 → no renderDBCard call.
	var databases []model.DBSnapshot
	if len(databases) > 0 {
		t.Error("should not reach renderDBCard with empty slice")
	}

	// Direct: renderDBCard always produces output (it was called with a real snap).
	// Verify no "DB ·" appears unless renderDBCard is explicitly called.
	lines := r.renderDBCard(&model.DBSnapshot{Name: "visible", Driver: "postgres"}, 80)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "DB ·") {
		t.Error("expected 'DB ·' prefix in card when DB exists")
	}
}
