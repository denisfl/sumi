// path: internal/config/config.go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Alerts holds configurable alert thresholds.
// A zero value means the alert is disabled.
type Alerts struct {
	CPUThreshold  float64 `toml:"cpu_threshold"`  // e.g. 90.0 = alert when CPU > 90%
	MemThreshold  float64 `toml:"mem_threshold"`  // e.g. 90.0 = alert when MEM > 90%
	DiskThreshold float64 `toml:"disk_threshold"` // e.g. 90.0 = alert when any disk > 90%
	TempThreshold float64 `toml:"temp_threshold"` // e.g. 80.0 = alert when CPU temp > 80°C
	Sound         bool    `toml:"sound"`          // emit \a bell when an alert is active
}

// Config holds all sumi runtime configuration.
// CLI flags override config file values.
type Config struct {
	Interval    int      `toml:"interval"`     // refresh seconds, default 5
	Renderer    string   `toml:"renderer"`     // "tui" | "json", default "tui"
	Theme       string   `toml:"theme"`        // default "tokyo-night"
	BorderStyle string   `toml:"border_style"` // "rounded" | "sharp" | "double" | "bold", default "rounded"
	CompactMode bool     `toml:"compact_mode"` // default false
	Widgets     []string `toml:"widgets"`      // card order, default all
	Alerts      Alerts   `toml:"alerts"`       // configurable alert thresholds
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Interval:    5,
		Renderer:    "tui",
		Theme:       "tokyo-night",
		BorderStyle: "rounded",
		CompactMode: false,
		Widgets:     []string{"thermal", "cpu", "memory", "disk", "network", "processes", "system"},
	}
}

// Load reads the config file from the standard location.
// If no file is found, Default() is returned with no error.
// If the file is found but malformed, an error is returned.
func Load() (Config, error) {
	path, ok := configPath()
	if !ok {
		return Default(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// File listed in dir but unreadable — surface the error
		return Default(), fmt.Errorf("sumi: cannot read config %s: %w", path, err)
	}

	cfg := Default()
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Default(), fmt.Errorf("sumi: invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// configPath returns the path to the config file if it exists.
func configPath() (string, bool) {
	dirs := []string{}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "sumi", "config.toml"))
	}

	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "sumi", "config.toml"))
	}

	for _, p := range dirs {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// DefaultConfigDir returns the preferred config directory for writing.
func DefaultConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "sumi"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "sumi"), nil
}

// DefaultConfigContent is the annotated TOML written by --init-config.
const DefaultConfigContent = `# sumi configuration — https://github.com/denisfl/sumi

# Refresh interval in seconds (watch mode)
interval = 5

# Output renderer: "tui" or "json"
renderer = "tui"

# Color theme. Built-in: tokyo-night, gruvbox, catppuccin-mocha, nord, dracula, one-dark
theme = "tokyo-night"

# Card border style: "rounded", "sharp", "double", "bold"
border_style = "rounded"

# Show compact (3-line) cards instead of full detail cards
compact_mode = false

# Card order (remove a name to hide that card)
widgets = ["thermal", "cpu", "memory", "disk", "network", "processes", "system"]

[alerts]
# Alert when CPU usage exceeds threshold (0 = disabled)
cpu_threshold = 0.0
# Alert when memory usage exceeds threshold (0 = disabled)
mem_threshold = 0.0
# Alert when any disk usage exceeds threshold (0 = disabled)
disk_threshold = 0.0
# Alert when CPU temperature exceeds threshold in °C (0 = disabled)
temp_threshold = 0.0
# Emit terminal bell (\a) when an alert is active
sound = false
`
