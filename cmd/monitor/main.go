// cmd/monitor/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sumi/internal/collector"
	"sumi/internal/renderer"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	watch := flag.Bool("watch", false, "enable continuous watch mode")
	flag.BoolVar(watch, "w", false, "enable continuous watch mode (shorthand)")

	interval := flag.Int("interval", 5, "update interval in seconds (watch mode)")
	flag.IntVar(interval, "n", 5, "update interval in seconds (shorthand)")

	rendererName := flag.String("renderer", "tui", "output renderer: tui | json")

	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("sumi", version)
		os.Exit(0)
	}

	if *interval < 1 {
		fmt.Fprintln(os.Stderr, "interval must be >= 1")
		os.Exit(1)
	}

	rdr, err := renderer.New(*rendererName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	col := collector.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !*watch {
		// Single-shot mode
		runOnce(ctx, col, rdr)
		return
	}

	// Watch mode
	renderer.HideCursor()
	defer renderer.ShowCursor()

	// Handle SIGINT / SIGTERM gracefully
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
