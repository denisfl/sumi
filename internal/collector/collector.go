// internal/collector/collector.go
package collector

import (
	"context"
	"time"

	"sumi/internal/model"
)

// Collector collects a system snapshot.
type Collector interface {
	Collect(ctx context.Context) (model.Snapshot, error)
}

// EventCollector is an optional interface collectors may implement to
// provide OS-level event detection. Implemented on Linux; returns an
// empty slice on all other platforms.
type EventCollector interface {
	CollectEvents(ctx context.Context, since time.Time) []model.SystemEvent
}
