// cmd/monitor/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"sumi/internal/collector"
	"sumi/internal/config"
	"sumi/internal/renderer"
	"sumi/internal/theme"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	// Load config file first (CLI flags override below)
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	watch := flag.Bool("watch", false, "enable continuous watch mode")
	flag.BoolVar(watch, "w", false, "enable continuous watch mode (shorthand)")

	interval := flag.Int("interval", cfg.Interval, "update interval in seconds (watch mode)")
	flag.IntVar(interval, "n", cfg.Interval, "update interval in seconds (shorthand)")

	rendererName := flag.String("renderer", cfg.Renderer, "output renderer: tui | json")
	compact := flag.Bool("compact", cfg.CompactMode, "use compact (3-line) card layout")
	themeName := flag.String("theme", cfg.Theme, "color theme name")
	borderStyle := flag.String("border", cfg.BorderStyle, "border style: rounded|sharp|double|bold")
	listThemes := flag.Bool("list-themes", false, "list available themes and exit")

	showVersion := flag.Bool("version", false, "print version and exit")
	initConfig := flag.Bool("init-config", false, "write default config to ~/.config/sumi/config.toml and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("sumi", version)
		os.Exit(0)
	}

	if *listThemes {
		for _, n := range theme.ListBuiltin() {
			fmt.Println(n)
		}
		return
	}

	if *initConfig {
		writeDefaultConfig()
		return
	}

	// CLI flags override config
	cfg.Renderer = *rendererName
	cfg.CompactMode = *compact

	if *interval < 1 {
		fmt.Fprintln(os.Stderr, "interval must be >= 1")
		os.Exit(1)
	}

	t, err := theme.Load(*themeName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "theme error: %v\n", err)
		os.Exit(1)
	}
	bc := theme.BoxStyle(*borderStyle)

	rdr, err := renderer.New(cfg, t, bc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	col := collector.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !*watch {
		runOnce(ctx, col, rdr)
		return
	}

	// Watch mode
	renderer.HideCursor()
	defer renderer.ShowCursor()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		renderer.ShowCursor()
		cancel()
		os.Exit(0)
	}()

	ticker := time.NewTicker(time.Duration(*interval) * time.Second)
	defer ticker.Stop()

	runOnce(ctx, col, rdr)
	for {
		select {
		case <-ticker.C:
			runOnce(ctx, col, rdr)
		case <-ctx.Done():
			return
		}
	}
}

func runOnce(ctx context.Context, col collector.Collector, rdr renderer.Renderer) {
	snap, err := col.Collect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect error: %v\n", err)
		return
	}
	if err := rdr.Render(snap); err != nil {
		fmt.Fprintf(os.Stderr, "render error: %v\n", err)
	}
}

func writeDefaultConfig() {
	dir, err := config.DefaultConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create config dir %s: %v\n", dir, err)
		os.Exit(1)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(config.DefaultConfigContent), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write config %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Println("config written to", path)
}
