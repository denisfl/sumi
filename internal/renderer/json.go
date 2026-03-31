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
