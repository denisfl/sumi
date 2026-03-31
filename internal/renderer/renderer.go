// internal/renderer/renderer.go
package renderer

import (
	"fmt"

	"sumi/internal/model"
)

// Renderer renders a system snapshot.
type Renderer interface {
	Render(s model.Snapshot) error
}

// New returns a renderer by name. Supported: "tui", "json".
func New(name string) (Renderer, error) {
	switch name {
	case "tui":
		return NewTUI(), nil
	case "json":
		return NewJSON(), nil
	default:
		return nil, fmt.Errorf("unknown renderer %q: choose tui or json", name)
	}
}
