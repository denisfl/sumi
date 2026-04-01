// internal/theme/theme.go
package theme

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed themes/*.toml
var builtinFS embed.FS

// Color holds an RGB triplet.
type Color struct {
	R uint8 `toml:"r"`
	G uint8 `toml:"g"`
	B uint8 `toml:"b"`
}

// ANSI returns the foreground ANSI TrueColor escape sequence.
func (c Color) ANSI() string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
}

// ANSIBg returns the background ANSI TrueColor escape sequence.
func (c Color) ANSIBg() string {
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B)
}

// Theme holds a complete color palette for the TUI renderer.
type Theme struct {
	Name   string `toml:"name"`
	Border Color  `toml:"border"`
	Title  Color  `toml:"title"`
	Text   Color  `toml:"text"`
	Green  Color  `toml:"green"`
	Yellow Color  `toml:"yellow"`
	Red    Color  `toml:"red"`
	Cyan   Color  `toml:"cyan"`
	Purple Color  `toml:"purple"`
	Orange Color  `toml:"orange"`
	Teal   Color  `toml:"teal"`
}

// BoxChars holds the border-drawing runes for a card style.
type BoxChars struct {
	TL, TR, BL, BR string // corners
	H, V           string // horizontal / vertical line
	TT, BT         string // top-T / bottom-T junction
}

// BoxStyle returns BoxChars for the named border style.
// Valid names: "rounded" (default), "sharp", "double", "bold".
func BoxStyle(name string) BoxChars {
	switch name {
	case "sharp":
		return BoxChars{"┌", "┐", "└", "┘", "─", "│", "┬", "┴"}
	case "double":
		return BoxChars{"╔", "╗", "╚", "╝", "═", "║", "╦", "╩"}
	case "bold":
		return BoxChars{"┏", "┓", "┗", "┛", "━", "┃", "┳", "┻"}
	default: // "rounded"
		return BoxChars{"╭", "╮", "╰", "╯", "─", "│", "┬", "┴"}
	}
}

// Load resolves a theme by name.
// It first checks the user config dir ($XDG_CONFIG_HOME/sumi/themes/<name>.toml),
// then falls back to the built-in embedded themes.
func Load(name string) (Theme, error) {
	// Try user theme dir first.
	if userDir, err := userThemeDir(); err == nil {
		path := filepath.Join(userDir, name+".toml")
		if data, err := os.ReadFile(path); err == nil {
			var t Theme
			if _, err := toml.Decode(string(data), &t); err != nil {
				return Theme{}, fmt.Errorf("theme %q: %w", name, err)
			}
			return t, nil
		}
	}

	// Try built-in themes.
	data, err := builtinFS.ReadFile("themes/" + name + ".toml")
	if err != nil {
		return Theme{}, fmt.Errorf("theme %q not found (built-ins: %s)", name, strings.Join(ListBuiltin(), ", "))
	}
	var t Theme
	if _, err := toml.Decode(string(data), &t); err != nil {
		return Theme{}, fmt.Errorf("theme %q: %w", name, err)
	}
	return t, nil
}

// ListBuiltin returns the names of all embedded built-in themes.
func ListBuiltin() []string {
	entries, _ := builtinFS.ReadDir("themes")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), ".toml")
		names = append(names, n)
	}
	return names
}

func userThemeDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "sumi", "themes"), nil
}
