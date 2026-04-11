// internal/collector/db/postgres_integration_test.go
//
// Integration tests that require the sumi-cloud docker-compose postgres to be running.
// Start it with: docker compose -f /Users/denis/dev/sumi/cloud/docker-compose.yml up db -d
//
// Run with:  go test -tags integration ./internal/collector/db/...
//
//go:build integration

package db

import (
	"context"
	"testing"
)

// cloudDSN points to the sumi-cloud docker-compose postgres (port 5433).
const cloudDSN = "host=localhost port=5433 user=sumi password=sumi_dev dbname=sumi sslmode=disable"

func TestPostgres_Collect_CloudDB(t *testing.T) {
	col, err := newPostgres("sumi-cloud", cloudDSN)
	if err != nil {
		t.Fatalf("newPostgres: %v", err)
	}
	defer col.Close()

	snap, err := col.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if snap.Name != "sumi-cloud" {
		t.Errorf("Name: got %q, want %q", snap.Name, "sumi-cloud")
	}
	if snap.Driver != "postgres" {
		t.Errorf("Driver: got %q, want postgres", snap.Driver)
	}
	if snap.Connections.Max <= 0 {
		t.Errorf("Connections.Max: got %d, expected > 0", snap.Connections.Max)
	}
	if snap.Error != "" {
		t.Errorf("unexpected Error: %s", snap.Error)
	}
}

// TestPostgres_Collect_CloudDB_SecondTick verifies delta metrics stabilise on the
// second collection (first tick always returns 0 throughput).
func TestPostgres_Collect_CloudDB_SecondTick(t *testing.T) {
	col, err := newPostgres("sumi-cloud", cloudDSN)
	if err != nil {
		t.Fatalf("newPostgres: %v", err)
	}
	defer col.Close()

	ctx := context.Background()

	// First tick — establishes baseline.
	if _, err := col.Collect(ctx); err != nil {
		t.Fatalf("first Collect: %v", err)
	}

	// Second tick — latency and connection counts should be stable.
	snap, err := col.Collect(ctx)
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if snap.Connections.Max <= 0 {
		t.Errorf("second tick Connections.Max: got %d, expected > 0", snap.Connections.Max)
	}
}
