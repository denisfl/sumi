// internal/renderer/renderer.go
package renderer

import (
	"fmt"

	"sumi/internal/config"
	"sumi/internal/model"
	"sumi/internal/theme"
)

// Renderer renders a system snapshot.
type Renderer interface {
	Render(s model.Snapshot) error
}

// New returns a renderer configured from cfg, theme, and border style.
func New(cfg config.Config, t theme.Theme, bc theme.BoxChars) (Renderer, error) {
	switch cfg.Renderer {
	case "tui":
		return NewTUI(cfg, t, bc), nil
	case "json":
		return NewJSON(), nil
	default:
		return nil, fmt.Errorf("unknown renderer %q: choose tui or json", cfg.Renderer)
	}
}
