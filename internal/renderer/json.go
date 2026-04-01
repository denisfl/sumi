// internal/renderer/json.go
package renderer

import (
	"encoding/json"
	"os"

	"sumi/internal/model"
)

type jsonRenderer struct{}

// NewJSON returns a renderer that marshals the snapshot to indented JSON on stdout.
func NewJSON() Renderer {
	return &jsonRenderer{}
}

func (r *jsonRenderer) Render(s model.Snapshot) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// ndjsonRenderer writes one compact JSON object per line to stdout (NDJSON / JSON-Lines).
// Suitable for streaming: sumi --watch --renderer json | jq '.CPU.Usage'
type ndjsonRenderer struct{}

// NewNDJSON returns a renderer that emits one compact JSON object per line, flushing immediately.
func NewNDJSON() Renderer {
	return &ndjsonRenderer{}
}

func (r *ndjsonRenderer) Render(s model.Snapshot) error {
	enc := json.NewEncoder(os.Stdout)
	// No indent — compact single-line output per tick.
	return enc.Encode(s)
}
