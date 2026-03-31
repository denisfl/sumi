// internal/collector/collector.go
package collector

import (
	"context"

	"sumi/internal/model"
)

// Collector collects a system snapshot.
type Collector interface {
	Collect(ctx context.Context) (model.Snapshot, error)
}
