// path: internal/config/config.go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Database holds connection settings for a single monitored database instance.
// The DSN may reference an environment variable ("${VAR}") or a file ("file:/path");
// plain connection strings are accepted for development but should be avoided in production.
type Database struct {
	Name      string `toml:"name"`       // display label in TUI / JSON output
	Driver    string `toml:"driver"`     // "postgres" | "mysql"
	DSN       string `toml:"dsn"`        // connection string or reference
	IntervalS int    `toml:"interval_s"` // collection interval; 0 uses the global interval
}

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
	Alerts      Alerts     `toml:"alerts"`      // configurable alert thresholds
	Databases   []Database `toml:"database"`    // optional [[database]] table array

	// Cloud push — opt-in background sync.
	PushEnabled  bool   `toml:"push_enabled"`
	PushURL      string `toml:"push_url"`
	PushToken    string `toml:"push_token"`
	PushInterval int    `toml:"push_interval"` // seconds between pushes; 0 = use Interval
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
		PushEnabled:  false,
		PushURL:      "https://ingest.getsumi.dev/v1/push",
		PushInterval: 60,
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

# ── Cloud push (optional) ───────────────────────────────────────────────────
# Set push_enabled = true and fill push_token to stream snapshots to sumi cloud.
# Get your token at https://app.getsumi.dev/settings/tokens
push_enabled  = false
push_url      = "https://ingest.getsumi.dev/v1/push"
push_token    = ""
push_interval = 60

# ── Database monitoring (optional) ──────────────────────────────────────────
# Add one [[database]] block per database to monitor.
# DSN may be a literal connection string, an env reference "${VAR}", or a
# file reference "file:/path/to/secret".

# [[database]]
# name       = "main-postgres"
# driver     = "postgres"
# dsn        = "${DATABASE_URL}"
# interval_s = 30

# [[database]]
# name       = "analytics"
# driver     = "mysql"
# dsn        = "${ANALYTICS_DSN}"
# interval_s = 60
`
