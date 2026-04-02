// cmd/monitor/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/term"

	"sumi/internal/collector"
	"sumi/internal/config"
	"sumi/internal/history"
	"sumi/internal/model"
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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sumi [flags]\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nNDJSON streaming (pipe-friendly):\n  sumi --watch --renderer json | jq '.CPU.Usage'\n")
	}
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

	// Spark rings — kept in watch mode; empty in single-shot mode.
	const ringCap = 120
	cpuRing := history.NewRing(ringCap)
	memRing := history.NewRing(ringCap)
	rxRing := history.NewRing(ringCap)
	txRing := history.NewRing(ringCap)

	if !*watch {
		runOnce(ctx, col, rdr, cpuRing, memRing, rxRing, txRing, nil)
		return
	}

	// Watch mode: when streaming JSON (NDJSON), skip TUI cursor/screen management
	// and emit one compact JSON object per line so the output is pipe-friendly.
	// Usage: sumi --watch --renderer json | jq '.CPU.Usage'
	isNDJSON := *rendererName == "json"
	if isNDJSON {
		rdr = renderer.NewNDJSON()
	}

	// Watch mode
	if !isNDJSON {
		renderer.HideCursor()
		defer renderer.ShowCursor()
	}

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

	// -- Interactive keyboard setup --
	// Mutable renderer state (theme, compact, sort column).
	activeThemeName := *themeName
	activeCompact := *compact
	activeSort := "cpu" // "cpu" or "mem"
	themes := theme.ListBuiltin()
	themeIdx := 0
	for i, tn := range themes {
		if tn == activeThemeName {
			themeIdx = i
			break
		}
	}
	selectedProc := 0 // j/k navigation index in proc table (-1 = no selection)
	detailPID := 0    // PID currently shown in detail panel; 0 = none

	// Per-PID CPU/Mem history rings (task 23).
	const pidRingCap = 60
	pidCPURings := make(map[int]*history.Ring)
	pidMemRings := make(map[int]*history.Ring)

	// killConfirm holds a pending kill request.
	// When non-nil, we show a confirmation line and wait for 'y'/'n'.
	type killReq struct {
		name string
		pid  int
	}
	var pendingKill *killReq

	rebuildRenderer := func() {
		t, err := theme.Load(activeThemeName)
		if err != nil {
			return
		}
		bc := theme.BoxStyle(*borderStyle)
		cfg.CompactMode = activeCompact
		rdr, err = renderer.New(cfg, t, bc)
		if err != nil {
			return
		}
	}

	// Enable raw terminal if stdin is a tty.
	var oldState *term.State
	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, _ = term.MakeRaw(int(os.Stdin.Fd()))
		defer func() {
			if oldState != nil {
				_ = term.Restore(int(os.Stdin.Fd()), oldState)
			}
		}()
	}

	keyCh := make(chan rune, 8)
	if oldState != nil {
		go func() {
			buf := make([]byte, 4)
			for {
				n, err := os.Stdin.Read(buf)
				if err != nil || n == 0 {
					return
				}
				keyCh <- rune(buf[0])
			}
		}()
	}

	// lastSnap is stored to reference proc list for kill confirmations.
	var lastSnap model.Snapshot

	handleKey := func(ch rune) {
		if pendingKill != nil {
			switch ch {
			case 'y', 'Y':
				if pendingKill.pid > 0 {
					_ = exec.Command("kill", "-TERM", fmt.Sprintf("%d", pendingKill.pid)).Run()
				}
			}
			pendingKill = nil
			return
		}
		switch ch {
		case 'q', 'Q', 3: // 3 = Ctrl+C
			if oldState != nil {
				_ = term.Restore(int(os.Stdin.Fd()), oldState)
			}
			renderer.ShowCursor()
			cancel()
			os.Exit(0)
		case 'v', 'V':
			activeCompact = !activeCompact
			rebuildRenderer()
		case 't', 'T':
			themeIdx = (themeIdx + 1) % len(themes)
			activeThemeName = themes[themeIdx]
			rebuildRenderer()
		case 'j', 'J':
			if selectedProc < len(lastSnap.Procs)-1 {
				selectedProc++
			}
		case 'k', 'K':
			if selectedProc > 0 {
				selectedProc--
			}
		case 's', 'S':
			if activeSort == "cpu" {
				activeSort = "mem"
			} else {
				activeSort = "cpu"
			}
		case 'd', 'D':
			if detailPID != 0 {
				detailPID = 0 // toggle off
			} else if selectedProc >= 0 && selectedProc < len(lastSnap.Procs) {
				detailPID = lastSnap.Procs[selectedProc].PID
			}
		case 27: // Esc — close detail panel
			detailPID = 0
		case 13: // Enter
			if selectedProc >= 0 && selectedProc < len(lastSnap.Procs) {
				p := lastSnap.Procs[selectedProc]
				pendingKill = &killReq{name: p.Name, pid: p.PID}
				// Print confirmation prompt at bottom.
				fmt.Fprintf(os.Stderr, "\r\nkill %s (PID %d)? [y/N] ", p.Name, p.PID)
				return
			}
		}
	}

	doRender := func(snap model.Snapshot) {
		lastSnap = snap
		// Update per-PID rings (task 23).
		for _, p := range snap.Procs {
			if _, ok := pidCPURings[p.PID]; !ok {
				pidCPURings[p.PID] = history.NewRing(pidRingCap)
				pidMemRings[p.PID] = history.NewRing(pidRingCap)
			}
			pidCPURings[p.PID].Push(p.CPUPct)
			pidMemRings[p.PID].Push(p.MemPct)
		}
		if err := rdr.Render(snap); err != nil {
			fmt.Fprintf(os.Stderr, "render error: %v\n", err)
		}
		// Detail panel (tasks 24/25): validate detailPID still alive.
		if detailPID != 0 {
			det := collector.ReadProcDetail(detailPID)
			if det == nil {
				detailPID = 0 // process gone
			} else {
				if cr, ok := pidCPURings[detailPID]; ok {
					det.CPUSpark = cr.Sparkline(30)
				}
				if mr, ok := pidMemRings[detailPID]; ok {
					det.MemSpark = mr.Sparkline(30)
				}
				renderer.RenderDetailPanel(*det)
			}
		}
	}

	runOnce(ctx, col, rdr, cpuRing, memRing, rxRing, txRing, doRender)

	// snapCh carries completed snapshots from the background collector goroutine.
	// Capacity 1 ensures the goroutine never blocks if the render loop is busy.
	snapCh := make(chan model.Snapshot, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				snap, err := col.Collect(ctx)
				if err != nil {
					continue
				}
				// Non-blocking send: drop the snapshot if the render loop hasn't
				// consumed the previous one yet (e.g. terminal is too slow).
				select {
				case snapCh <- snap:
				default:
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case snap := <-snapCh:
			cpuRing.Push(snap.CPU.Usage)
			if snap.Mem.TotalBytes > 0 {
				memRing.Push(float64(snap.Mem.UsedBytes) / float64(snap.Mem.TotalBytes) * 100.0)
			}
			rxRing.Push(snap.Net.RxKBps)
			txRing.Push(snap.Net.TxKBps)
			const sparkWidth = 30
			snap.History.CPUSpark = cpuRing.Sparkline(sparkWidth)
			snap.History.MemSpark = memRing.Sparkline(sparkWidth)
			snap.History.NetRxSpark = rxRing.Sparkline(sparkWidth)
			snap.History.NetTxSpark = txRing.Sparkline(sparkWidth)
			doRender(snap)
		case ch := <-keyCh:
			handleKey(ch)
		case <-ctx.Done():
			return
		}
	}
}

func runOnce(ctx context.Context, col collector.Collector, rdr renderer.Renderer,
	cpuRing, memRing, rxRing, txRing *history.Ring, renderFn func(model.Snapshot)) {
	snap, err := col.Collect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect error: %v\n", err)
		return
	}
	cpuRing.Push(snap.CPU.Usage)
	if snap.Mem.TotalBytes > 0 {
		memRing.Push(float64(snap.Mem.UsedBytes) / float64(snap.Mem.TotalBytes) * 100.0)
	}
	rxRing.Push(snap.Net.RxKBps)
	txRing.Push(snap.Net.TxKBps)
	const sparkWidth = 30
	snap.History.CPUSpark = cpuRing.Sparkline(sparkWidth)
	snap.History.MemSpark = memRing.Sparkline(sparkWidth)
	snap.History.NetRxSpark = rxRing.Sparkline(sparkWidth)
	snap.History.NetTxSpark = txRing.Sparkline(sparkWidth)

	if renderFn != nil {
		renderFn(snap)
	} else if err := rdr.Render(snap); err != nil {
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
