// internal/renderer/tui.go
package renderer

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
	"sumi/internal/config"
	"sumi/internal/model"
	"sumi/internal/theme"
)

// out is a 64 KiB buffered writer over os.Stdout.
// All TUI output goes through it; Render() flushes once per frame.
var out = bufio.NewWriterSize(os.Stdout, 64*1024)

// Immutable ANSI codes — not theme-configurable.
const (
colReset = "\x1b[0m"
colDim   = "\x1b[2m"

cursorHide = "\x1b[?25l"
cursorShow = "\x1b[?25h"
clearAll   = "\x1b[2J\x1b[H"
)

// themeColors holds pre-computed ANSI escape strings for the active theme.
type themeColors struct {
border, title, text, green, yellow, red, cyan, purple, orange, teal string
}

func newThemeColors(t theme.Theme) themeColors {
return themeColors{
border: t.Border.ANSI(),
title:  t.Title.ANSI(),
text:   t.Text.ANSI(),
green:  t.Green.ANSI(),
yellow: t.Yellow.ANSI(),
red:    t.Red.ANSI(),
cyan:   t.Cyan.ANSI(),
purple: t.Purple.ANSI(),
orange: t.Orange.ANSI(),
teal:   t.Teal.ANSI(),
}
}

// tuiRenderer renders snapshots using a configurable theme and border style.
type tuiRenderer struct {
	compact      bool
	tc           themeColors
	box          theme.BoxChars
	cfg          config.Config
	activeAlerts int // count of currently breached thresholds
	flashOn      bool // true on odd seconds (for border flash)
}

// NewTUI returns a TUI renderer with the given config, theme, and border style.
func NewTUI(cfg config.Config, t theme.Theme, bc theme.BoxChars) Renderer {
	return &tuiRenderer{
		compact: cfg.CompactMode,
		tc:      newThemeColors(t),
		box:     bc,
		cfg:     cfg,
	}
}

func (r *tuiRenderer) Render(s model.Snapshot) error {
	fmt.Fprint(out, clearAll)
	r.computeAlerts(s)
	width := terminalWidth()
	if width < 40 {
		width = 80
	}
	var err error
	if r.compact {
		err = r.renderCompact(s, width)
	} else {
		err = r.renderFull(s, width)
	}
	_ = out.Flush()
	return err
}

// computeAlerts evaluates all configured thresholds and sets r.activeAlerts / r.flashOn.
func (r *tuiRenderer) computeAlerts(s model.Snapshot) {
	a := r.cfg.Alerts
	r.activeAlerts = 0
	r.flashOn = time.Now().Second()%2 == 1

	if a.CPUThreshold > 0 && s.CPU.Usage > a.CPUThreshold {
		r.activeAlerts++
	}
	if a.MemThreshold > 0 && s.Mem.TotalBytes > 0 {
		memPct := float64(s.Mem.UsedBytes) / float64(s.Mem.TotalBytes) * 100.0
		if memPct > a.MemThreshold {
			r.activeAlerts++
		}
	}
	if a.DiskThreshold > 0 {
		for _, d := range s.Disks {
			if d.TotalBytes > 0 {
				diskPct := float64(d.UsedBytes) / float64(d.TotalBytes) * 100.0
				if diskPct > a.DiskThreshold {
					r.activeAlerts++
					break
				}
			}
		}
	}
	if a.TempThreshold > 0 {
		for _, sensor := range s.Thermal.Sensors {
			if sensor.TempC > a.TempThreshold {
				r.activeAlerts++
				break
			}
		}
	}
	// Bell: emit once per cycle when any alert is active and sound is enabled.
	if r.activeAlerts > 0 && a.Sound {
		fmt.Fprint(out, "\a")
	}
}

func (r *tuiRenderer) renderFull(s model.Snapshot, width int) error {
	// Grid: left col ≈ 1/3, right col ≈ 2/3.
	narrow := (width - 3) / 3
	if narrow < 20 {
		narrow = 20
	}
	wide := width - 3 - narrow
	if wide < narrow {
		wide = narrow
	}

	// Row 1: Thermal (narrow) + CPU (wide)
	printRow(r.renderThermalCard(s, narrow), r.renderCPUCard(s, wide))
	fmt.Fprint(out, "\r\n")

	// GPU card (full width, optional)
	if s.GPU != nil {
		printCard(r.renderGPUCard(s, width))
		fmt.Fprint(out, "\r\n")
	}

	// Row 2: Memory (narrow) + Disk (wide)
	printRow(r.renderMemCard(s, narrow), r.renderDiskCard(s, wide))
	fmt.Fprint(out, "\r\n")

	// Row 3: Network (narrow) + Top Processes (wide)
	printRow(r.renderNetCard(s, narrow), r.renderProcsCard(s, wide))
	fmt.Fprint(out, "\r\n")

	// Battery card (full width, optional)
	if s.Battery != nil {
		printCard(r.renderBatteryCard(s, width))
		fmt.Fprint(out, "\r\n")
	}

	// Row 4: System (full width)
	printCard(r.renderSystemCard(s, width))
	return nil
}

// RenderDetailPanel prints a process detail overlay panel to stdout.
// It is designed to be called after Render() to append the panel below the main view.
func RenderDetailPanel(det model.ProcDetail) {
	w := terminalWidth()
	if w < 40 {
		w = 80
	}
	inner := w - 2

	top := "\x1b[2m╭" + strings.Repeat("─", inner) + "╮\x1b[0m"
	bot := "\x1b[2m╰" + strings.Repeat("─", inner) + "╯\x1b[0m"
	sep := "\x1b[2m├" + strings.Repeat("─", inner) + "┤\x1b[0m"
	line := func(content string) string {
		vis := visibleLen(content)
		if vis < inner {
			content += strings.Repeat(" ", inner-vis)
		}
		return fmt.Sprintf("\x1b[2m│\x1b[0m%s\x1b[2m│\x1b[0m", content)
	}

	title := fmt.Sprintf(" \x1b[36mProcess Detail\x1b[0m  \x1b[2m[d] close\x1b[0m")
	fmt.Fprint(out, top+"\r\n")
	fmt.Fprint(out, line(title)+"\r\n")
	fmt.Fprint(out, sep+"\r\n")

	row := func(label, val string) {
		content := fmt.Sprintf(" \x1b[2m%-12s\x1b[0m \x1b[36m%s\x1b[0m", label, val)
		fmt.Fprint(out, line(content)+"\r\n")
	}

	row("pid", strconv.Itoa(det.PID))
	row("name", det.Name)
	row("ppid", strconv.Itoa(det.PPID))
	row("threads", strconv.Itoa(det.Threads))
	row("open fds", strconv.Itoa(det.FDs))
	row("cwd", det.Cwd)
	row("started", det.StartTime)
	if det.CPUSpark != "" {
		row("cpu history", "\x1b[32m"+det.CPUSpark+"\x1b[0m")
	}
	if det.MemSpark != "" {
		row("mem history", "\x1b[33m"+det.MemSpark+"\x1b[0m")
	}

	fmt.Fprint(out, bot+"\r\n")
	_ = out.Flush()
}

func (r *tuiRenderer) renderCompact(s model.Snapshot, width int) error {
half := (width - 3) / 2
printRow(
r.renderCompactCard("THERMAL", r.compactThermalLine(s), 0, half),
r.renderCompactCard("CPU", r.compactCPULine(s), s.CPU.Usage, half),
)
fmt.Fprint(out, "\r\n")
memPct := 0.0
if s.Mem.TotalBytes > 0 {
memPct = float64(s.Mem.UsedBytes) / float64(s.Mem.TotalBytes) * 100.0
}
diskPct := 0.0
if len(s.Disks) > 0 {
	d := s.Disks[0]
	if d.TotalBytes > 0 {
		diskPct = float64(d.UsedBytes) / float64(d.TotalBytes) * 100.0
	}
}
printRow(
r.renderCompactCard("MEM", r.compactMemLine(s, memPct), memPct, half),
r.renderCompactCard("DISK", r.compactDiskLine(s, diskPct), diskPct, half),
)
fmt.Fprint(out, "\r\n")
printRow(
r.renderCompactCard("NET", r.compactNetLine(s), -1, half),
r.renderCompactCard("SYSTEM", r.compactSysLine(s), -1, half),
)
return nil
}

// ---- Compact helpers ----

func (r *tuiRenderer) renderCompactCard(title, metricLine string, pct float64, w int) []string {
lines := []string{r.cardTop(title, w), r.cardLine(metricLine, w)}
if pct >= 0 {
barW := w - 4
if barW < 2 {
barW = 2
}
lines = append(lines, r.cardLine(r.progressBar(pct/100.0, barW), w))
} else {
lines = append(lines, r.cardEmpty(w))
}
lines = append(lines, r.cardBottom(w))
return lines
}

func (r *tuiRenderer) compactCPULine(s model.Snapshot) string {
return fmt.Sprintf(" %s%-6s%s %s%.1f%%%s  %s%d cores%s",
colDim, "load", colReset, r.pctColor(s.CPU.Usage), s.CPU.Usage, colReset,
r.tc.text, s.CPU.Cores, colReset)
}

func (r *tuiRenderer) compactMemLine(s model.Snapshot, pct float64) string {
return fmt.Sprintf(" %s%-3s%s %s%.1f%%%s  %s%s / %s%s",
colDim, "mem", colReset, r.pctColor(pct), pct, colReset,
r.pctColor(pct), fmtBytes(s.Mem.UsedBytes), r.tc.text, fmtBytes(s.Mem.TotalBytes))
}

func (r *tuiRenderer) compactDiskLine(s model.Snapshot, pct float64) string {
	mnt := "N/A"
	if len(s.Disks) > 0 {
		mnt = s.Disks[0].MountPoint
	}
	return fmt.Sprintf(" %s%-4s%s %s%s%s  %s%.1f%%%s",
		colDim, "mnt", colReset, r.tc.text, mnt, colReset,
		r.pctColor(pct), pct, colReset)
}

func (r *tuiRenderer) compactNetLine(s model.Snapshot) string {
iface := s.Net.Interface
if iface == "" {
iface = "N/A"
}
return fmt.Sprintf(" %s%s%s  %s%.1f%s %s%.1f KB/s%s",
r.tc.text, iface, colReset,
r.tc.green, s.Net.RxKBps, colReset,
r.tc.orange, s.Net.TxKBps, colReset)
}

func (r *tuiRenderer) compactSysLine(s model.Snapshot) string {
host := s.Hostname
if host == "" {
host = "N/A"
}
upt := s.Uptime
if upt == "" {
upt = "N/A"
}
return fmt.Sprintf(" %s%s%s  %s%s%s", r.tc.cyan, host, colReset, r.tc.green, upt, colReset)
}

func (r *tuiRenderer) compactThermalLine(s model.Snapshot) string {
	if len(s.Thermal.Sensors) > 0 {
		// Show CPU sensor primarily; fall back to first available.
		for _, sensor := range s.Thermal.Sensors {
			if sensor.Name == "CPU" {
				return fmt.Sprintf(" %s%-4s%s %s%.1f\u00b0C%s",
					colDim, "cpu", colReset, r.thermalColor(sensor.TempC), sensor.TempC, colReset)
			}
		}
		first := s.Thermal.Sensors[0]
		return fmt.Sprintf(" %s%-4s%s %s%.1f\u00b0C%s",
			colDim, first.Name, colReset, r.thermalColor(first.TempC), first.TempC, colReset)
	}
	if s.Thermal.TempC > 0 {
		return fmt.Sprintf(" %s%-4s%s %s%.1f\u00b0C%s",
			colDim, "temp", colReset, r.thermalColor(s.Thermal.TempC), s.Thermal.TempC, colReset)
	}
	return fmt.Sprintf(" %s%s%s", colDim, "no temp data", colReset)
}

// ---- Layout helpers ----

func printCard(lines []string) {
for _, l := range lines {
fmt.Fprint(out, l+"\r\n")
}
}

func printRow(left, right []string) {
maxLines := len(left)
if len(right) > maxLines {
maxLines = len(right)
}
leftW, rightW := 0, 0
if len(left) > 0 {
leftW = visibleLen(left[0])
}
if len(right) > 0 {
rightW = visibleLen(right[0])
}
blankLeft := strings.Repeat(" ", leftW)
blankRight := strings.Repeat(" ", rightW)
for i := 0; i < maxLines; i++ {
l, r := blankLeft, blankRight
if i < len(left) {
l = left[i]
}
if i < len(right) {
r = right[i]
}
		fmt.Fprint(out, l+" "+r+"\r\n")
}
}

// ---- Full-mode card renderers ----

func (r *tuiRenderer) renderCPUCard(s model.Snapshot, w int) []string {
	cpuAlert := r.cfg.Alerts.CPUThreshold > 0 && s.CPU.Usage > r.cfg.Alerts.CPUThreshold
	lines := []string{r.cardTopAlert("CPU", w, cpuAlert), r.cardSep(w)}

	// Headline: big % + per-core mini blocks + core count.
	pctStr := fmt.Sprintf("%s%.1f%%%s", r.pctColor(s.CPU.Usage), s.CPU.Usage, colReset)
	blocks := []string{" ", "\u2581", "\u2582", "\u2583", "\u2584", "\u2585", "\u2586", "\u2587", "\u2588"}
	micro := ""
	if len(s.CPU.CoreUsages) > 0 {
		for _, u := range s.CPU.CoreUsages {
			idx := int(u / 100.0 * 8.0)
			if idx > 8 {
				idx = 8
			}
			micro += blocks[idx]
		}
	}
	coresLabel := fmt.Sprintf("%s%d cores%s", colDim, s.CPU.Cores, colReset)
	if micro != "" {
		headline := fmt.Sprintf(" %-10s %s%s%s  %s", pctStr, r.pctColor(s.CPU.Usage), micro, colReset, coresLabel)
		lines = append(lines, r.cardLine(headline, w))
	} else {
		headline := fmt.Sprintf(" %-10s  %s", pctStr, coresLabel)
		lines = append(lines, r.cardLine(headline, w))
	}

	// Full-width progress bar.
	barW := w - 4
	if barW < 2 {
		barW = 2
	}
	lines = append(lines, r.cardLine(" "+r.progressBar(s.CPU.Usage/100.0, barW), w))

	// Sparkline only when per-core data AND spark history are available.
	if len(s.CPU.CoreUsages) > 0 && s.History.CPUSpark != "" {
		lines = append(lines, r.cardLine(fmt.Sprintf(" %s%s%s", r.pctColor(s.CPU.Usage), s.History.CPUSpark, colReset), w))
	} else {
		lines = append(lines, r.cardEmpty(w))
	}
	lines = append(lines, r.cardBottom(w))
	return lines
}

func (r *tuiRenderer) renderMemCard(s model.Snapshot, w int) []string {
	var pct float64
	if s.Mem.TotalBytes > 0 {
		pct = float64(s.Mem.UsedBytes) / float64(s.Mem.TotalBytes) * 100.0
	}
	memAlert := r.cfg.Alerts.MemThreshold > 0 && pct > r.cfg.Alerts.MemThreshold
	lines := []string{r.cardTopAlert("Memory", w, memAlert), r.cardSep(w)}

	// Headline: big % as primary accent.
	lines = append(lines, r.cardLine(fmt.Sprintf(" %s%.1f%%%s", r.pctColor(pct), pct, colReset), w))
	lines = append(lines, r.cardLine(fmt.Sprintf(" %s%s%s / %s%s%s",
		r.pctColor(pct), fmtBytes(s.Mem.UsedBytes), colReset,
		r.tc.text, fmtBytes(s.Mem.TotalBytes), colReset), w))

	// Full-width bar.
	barW := w - 4
	if barW < 2 {
		barW = 2
	}
	lines = append(lines, r.cardLine(" "+r.progressBar(pct/100.0, barW), w))

	// Swap row (when available): % + absolute values (if they fit) + bar.
	if s.Mem.SwapTotal > 0 {
		swapPct := float64(s.Mem.SwapUsed) / float64(s.Mem.SwapTotal) * 100.0
		swapBarW := w - 4
		if swapBarW < 2 {
			swapBarW = 2
		}
		// Build the full line; fall back to % only when sizes don't fit.
		fullLine := fmt.Sprintf(" %sswap%s  %s%.1f%%%s  %s%s / %s%s",
			colDim, colReset,
			r.pctColor(swapPct), swapPct, colReset,
			colDim, fmtBytes(s.Mem.SwapUsed), fmtBytes(s.Mem.SwapTotal), colReset)
		if visibleLen(fullLine) > w-2 {
			fullLine = fmt.Sprintf(" %sswap%s  %s%.1f%%%s",
				colDim, colReset, r.pctColor(swapPct), swapPct, colReset)
		}
		lines = append(lines, r.cardLine(fullLine, w))
		lines = append(lines, r.cardLine(" "+r.progressBar(swapPct/100.0, swapBarW), w))
	}

	lines = append(lines, r.cardBottom(w))
	return lines
}

func (r *tuiRenderer) renderDiskCard(s model.Snapshot, w int) []string {
	count := len(s.Disks)
	titleSuffix := ""
	if count > 1 {
		titleSuffix = fmt.Sprintf(" %d", count)
	}
	lines := []string{r.cardTop("Disk"+titleSuffix, w)}

	if count == 0 {
		lines = append(lines, r.cardEmpty(w))
		lines = append(lines, r.cardBottom(w))
		return lines
	}

	// Show up to 4 disks; each entry: 2 lines — (mount+pct+total) and bar.
	for i, d := range s.Disks {
		if i >= 4 {
			break
		}
		var pct float64
		if d.TotalBytes > 0 {
			pct = float64(d.UsedBytes) / float64(d.TotalBytes) * 100.0
		}
		barW := w - 4
		if barW < 2 {
			barW = 2
		}
		if i > 0 {
			lines = append(lines, r.cardSep(w))
		}
		// Line 1: mount (truncated)  pct%  ·  total
		// suffix = "  pct%  ·  total", compute its visible length to truncate mount.
		suffix := fmt.Sprintf("  %s%.1f%%%s  %s\u00b7%s  %s%s%s",
			r.pctColor(pct), pct, colReset,
			colDim, colReset,
			r.tc.text, fmtBytes(d.TotalBytes), colReset)
		suffixVis := visibleLen(suffix)
		mountMaxW := w - 2 - suffixVis
		if mountMaxW < 3 {
			mountMaxW = 3
		}
		mnt := d.MountPoint
		if len(mnt) > mountMaxW {
			mnt = "\u2026" + mnt[len(mnt)-mountMaxW+1:]
		}
		lines = append(lines, r.cardLine(fmt.Sprintf("%s%s%s%s", r.tc.cyan, mnt, colReset, suffix), w))
		// Line 2: progress bar + optional I/O speed.
		barLine := " " + r.progressBar(pct/100.0, barW)
		if d.ReadKBps > 0 || d.WriteKBps > 0 {
			ioStr := fmt.Sprintf("  %s↓%s %s%s%s  %s↑%s %s%s%s",
				r.tc.green, colReset, colDim, fmtKBps(d.ReadKBps), colReset,
				r.tc.orange, colReset, colDim, fmtKBps(d.WriteKBps), colReset)
			barLine += ioStr
		}
		lines = append(lines, r.cardLine(barLine, w))
	}

	lines = append(lines, r.cardBottom(w))
	return lines
}

func (r *tuiRenderer) renderNetCard(s model.Snapshot, w int) []string {
iface := s.Net.Interface
if iface == "" {
iface = "N/A"
}
ip := s.Net.IP
if ip == "" {
ip = "N/A"
}
lines := []string{r.cardTop("Network", w)}
lines = append(lines, r.cardLine(fmt.Sprintf("%sIface:%s %s%s%s", r.tc.cyan, colReset, r.tc.text, iface, colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sIP:%s    %s%s%s", r.tc.cyan, colReset, r.tc.text, ip, colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sRx:%s    %s%.2f KB/s%s", r.tc.cyan, colReset, r.tc.green, s.Net.RxKBps, colReset), w))
if s.History.NetRxSpark != "" {
	lines = append(lines, r.cardLine(fmt.Sprintf(" %s%s%s", r.tc.green, s.History.NetRxSpark, colReset), w))
}
lines = append(lines, r.cardLine(fmt.Sprintf("%sTx:%s    %s%.2f KB/s%s", r.tc.cyan, colReset, r.tc.orange, s.Net.TxKBps, colReset), w))
if s.History.NetTxSpark != "" {
	lines = append(lines, r.cardLine(fmt.Sprintf(" %s%s%s", r.tc.orange, s.History.NetTxSpark, colReset), w))
}
lines = append(lines, r.cardBottom(w))
return lines
}

func (r *tuiRenderer) renderProcsCard(s model.Snapshot, w int) []string {
lines := []string{r.cardTop("Top Processes", w)}
header := fmt.Sprintf("%s%-20s %6s %6s%s", colDim+r.tc.text, "NAME", "CPU%", "MEM%", colReset)
lines = append(lines, r.cardLine(header, w))
if len(s.Procs) == 0 {
lines = append(lines, r.cardLine(fmt.Sprintf("%sN/A%s", colDim, colReset), w))
}
for _, p := range s.Procs {
name := p.Name
badge := ""
switch p.Container {
case "docker":
	badge = "[d] "
case "k8s":
	badge = "[k] "
}
maxName := 20 - len(badge)
if len(name) > maxName {
	name = name[:maxName-1] + "\u2026"
}
displayName := badge + name
row := fmt.Sprintf("%-20s %s%6.1f%s %s%6.1f%s",
displayName, r.pctColor(p.CPUPct), p.CPUPct, colReset,
r.tc.teal, p.MemPct, colReset)
lines = append(lines, r.cardLine(row, w))
}
lines = append(lines, r.cardBottom(w))
return lines
}

func (r *tuiRenderer) renderThermalCard(s model.Snapshot, w int) []string {
	tempAlert := false
	if r.cfg.Alerts.TempThreshold > 0 {
		for _, sensor := range s.Thermal.Sensors {
			if sensor.TempC > r.cfg.Alerts.TempThreshold {
				tempAlert = true
				break
			}
		}
	}
	lines := []string{r.cardTopAlert("THERMAL", w, tempAlert), r.cardSep(w)}

	if len(s.Thermal.Sensors) > 0 {
		// Show up to 3 named sensor rows (CPU, GPU, SSD).
		for i, sensor := range s.Thermal.Sensors {
			if i >= 3 {
				break
			}
			col := r.thermalColor(sensor.TempC)
			line := fmt.Sprintf(" %s%-6s%s %s%.1f\u00b0C%s",
				colDim, sensor.Name, colReset, col, sensor.TempC, colReset)
			lines = append(lines, r.cardLine(line, w))
		}
		// Pad to consistent height (3 sensor rows).
		for len(lines) < 5 {
			lines = append(lines, r.cardEmpty(w))
		}
		if s.Platform == "rpi" {
			thrStr, thrCol := "ok", r.tc.green
			if s.Thermal.Throttled != "" {
				thrStr = s.Thermal.Throttled
				thrCol = r.tc.red
			}
			lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s",
				colDim, "thrt", colReset, thrCol, thrStr, colReset), w))
		} else {
			lines = append(lines, r.cardEmpty(w))
		}
	} else {
		// Fallback: single TempC value.
		tempStr := "N/A"
		tempCol := r.tc.text
		if s.Thermal.TempC > 0 {
			tempStr = fmt.Sprintf("%.1f\u00b0C", s.Thermal.TempC)
			tempCol = r.thermalColor(s.Thermal.TempC)
		}
		if s.Thermal.TempC > 0 {
			lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s",
				colDim, "temp", colReset, tempCol, tempStr, colReset), w))
		} else {
			lines = append(lines, r.cardEmpty(w))
		}
		if s.Platform == "rpi" {
			armStr, gpuStr := "N/A", "N/A"
			if s.Thermal.ArmFreqMHz > 0 {
				armStr = fmt.Sprintf("%d MHz", s.Thermal.ArmFreqMHz)
			}
			if s.Thermal.GpuFreqMHz > 0 {
				gpuStr = fmt.Sprintf("%d MHz", s.Thermal.GpuFreqMHz)
			}
			thrStr, thrCol := "ok", r.tc.green
			if s.Thermal.Throttled != "" {
				thrStr = s.Thermal.Throttled
				thrCol = r.tc.red
			}
			lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s",
				colDim, "arm", colReset, r.tc.orange, armStr, colReset), w))
			lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s",
				colDim, "gpu", colReset, r.tc.purple, gpuStr, colReset), w))
			lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s",
				colDim, "thrt", colReset, thrCol, thrStr, colReset), w))
		} else {
			lines = append(lines, r.cardEmpty(w))
			lines = append(lines, r.cardEmpty(w))
			lines = append(lines, r.cardEmpty(w))
		}
	}

	lines = append(lines, r.cardBottom(w))
	return lines
}

func (r *tuiRenderer) renderGPUCard(s model.Snapshot, w int) []string {
	gpu := s.GPU
	lines := []string{r.cardTop("GPU", w), r.cardSep(w)}
	if gpu == nil {
		lines = append(lines, r.cardEmpty(w))
		lines = append(lines, r.cardBottom(w))
		return lines
	}
	name := gpu.Name
	if name == "" {
		name = gpu.Driver
	}
	lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-7s%s %s%s%s",
		colDim, "name", colReset, r.tc.cyan, name, colReset), w))
	lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-7s%s %s%.1f%%%s",
		colDim, "usage", colReset, r.pctColor(gpu.UsagePct), gpu.UsagePct, colReset), w))
	if gpu.TempC > 0 {
		lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-7s%s %s%.1f\u00b0C%s",
			colDim, "temp", colReset, r.thermalColor(gpu.TempC), gpu.TempC, colReset), w))
	} else {
		lines = append(lines, r.cardEmpty(w))
	}
	if gpu.VRAMTotalMiB > 0 {
		vramPct := float64(gpu.VRAMUsedMiB) / float64(gpu.VRAMTotalMiB) * 100.0
		lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-7s%s %s%d MiB%s / %s%d MiB%s  %s%.1f%%%s",
			colDim, "vram", colReset,
			r.pctColor(vramPct), gpu.VRAMUsedMiB, colReset,
			r.tc.text, gpu.VRAMTotalMiB, colReset,
			r.pctColor(vramPct), vramPct, colReset), w))
	} else {
		lines = append(lines, r.cardEmpty(w))
	}
	lines = append(lines, r.cardBottom(w))
	return lines
}

func (r *tuiRenderer) renderBatteryCard(s model.Snapshot, w int) []string {
	b := s.Battery
	lines := []string{r.cardTop("Battery", w), r.cardSep(w)}
	if b == nil {
		lines = append(lines, r.cardEmpty(w))
		lines = append(lines, r.cardBottom(w))
		return lines
	}

	// Headline: percentage + status
	status := "Discharging"
	if b.Charging {
		status = "Charging"
	}
	headline := fmt.Sprintf(" %s%.0f%%%s  %s%s%s",
		r.pctColorInv(b.ChargePct), b.ChargePct, colReset,
		r.tc.text, status, colReset)
	if b.TimeLeft != "" {
		headline = fmt.Sprintf(" %s%.0f%%%s  %s%s%s  %s%s%s",
			r.pctColorInv(b.ChargePct), b.ChargePct, colReset,
			r.tc.text, status, colReset,
			colDim, b.TimeLeft, colReset)
	}
	lines = append(lines, r.cardLine(headline, w))

	// Progress bar
	barW := w - 4
	if barW < 2 {
		barW = 2
	}
	lines = append(lines, r.cardLine(" "+r.progressBar(b.ChargePct/100.0, barW), w))
	lines = append(lines, r.cardBottom(w))
	return lines
}

func (r *tuiRenderer) renderSystemCard(s model.Snapshot, w int) []string {
	host := s.Hostname
	if host == "" {
		host = "N/A"
	}
	platStr := s.Platform
	if platStr == "" {
		platStr = "N/A"
	}
	upt := s.Uptime
	if upt == "" {
		upt = "N/A"
	}
	dateStr := s.Timestamp.Format("2006-01-02")
	timeStr := s.Timestamp.Format("15:04:05")

	lines := []string{r.cardTop("System", w)}

	// Row 1: host  platform  date — aligned columns, label width=10.
	row1 := fmt.Sprintf(" %s%-10s%s %s%-20s%s  %s%-10s%s %s%-12s%s  %s%-5s%s %s%s%s",
		colDim, "host", colReset, r.tc.cyan, host, colReset,
		colDim, "platform", colReset, r.tc.text, platStr, colReset,
		colDim, "date", colReset, r.tc.text, dateStr, colReset)
	lines = append(lines, r.cardLine(row1, w))

	// Row 2: uptime  time  (alerts if configured).
	a := r.cfg.Alerts
	anyThreshold := a.CPUThreshold > 0 || a.MemThreshold > 0 || a.DiskThreshold > 0 || a.TempThreshold > 0
	row2 := fmt.Sprintf(" %s%-10s%s %s%-20s%s  %s%-10s%s %s%s%s",
		colDim, "uptime", colReset, r.tc.green, upt, colReset,
		colDim, "time", colReset, r.tc.text, timeStr, colReset)
	if anyThreshold {
		alertStr := "none"
		alertCol := r.tc.green
		if r.activeAlerts > 0 {
			alertStr = fmt.Sprintf("%d ACTIVE", r.activeAlerts)
			alertCol = r.tc.red
		}
		row2 += fmt.Sprintf("  %s%-10s%s %s%s%s",
			colDim, "alerts", colReset, alertCol, alertStr, colReset)
	}
	lines = append(lines, r.cardLine(row2, w))

	lines = append(lines, r.cardBottom(w))
	return lines
}

// ---- Card border helpers ----

func (r *tuiRenderer) cardTop(title string, w int) string {
	return r.cardTopColored(title, w, r.tc.border)
}

// cardTopAlert renders the top border in the theme's red color when an alert is flashing.
func (r *tuiRenderer) cardTopAlert(title string, w int, alertActive bool) string {
	col := r.tc.border
	if alertActive && r.flashOn {
		col = r.tc.red
	}
	return r.cardTopColored(title, w, col)
}

func (r *tuiRenderer) cardTopColored(title string, w int, borderCol string) string {
	inner := w - 2
	titleFormatted := fmt.Sprintf(" %s%s%s ", r.tc.title, title, borderCol)
	titleVisLen := len(title) + 2
	dashCount := inner - titleVisLen
	if dashCount < 0 {
		dashCount = 0
	}
	rightDash := dashCount - 1
	if rightDash < 0 {
		rightDash = 0
	}
	return fmt.Sprintf("%s%s%s%s%s%s%s",
		borderCol, r.box.TL,
		strings.Repeat(r.box.H, 1),
		titleFormatted,
		strings.Repeat(r.box.H, rightDash),
		r.box.TR, colReset)
}

func (r *tuiRenderer) cardBottom(w int) string {
return fmt.Sprintf("%s%s%s%s%s", r.tc.border, r.box.BL, strings.Repeat(r.box.H, w-2), r.box.BR, colReset)
}

func (r *tuiRenderer) cardSep(w int) string {
return fmt.Sprintf("%s%s%s%s%s", r.tc.border, "\u251c", strings.Repeat(r.box.H, w-2), "\u2524", colReset)
}

func (r *tuiRenderer) cardEmpty(w int) string {
return r.cardLine("", w)
}

func (r *tuiRenderer) cardLine(content string, w int) string {
innerW := w - 2
visible := visibleLen(content)
var padded string
if visible < innerW {
padded = content + strings.Repeat(" ", innerW-visible)
} else {
padded = content
}
return fmt.Sprintf("%s%s%s%s%s%s%s", r.tc.border, r.box.V, colReset, padded, r.tc.border, r.box.V, colReset)
}

// ---- Progress bar ----

func (r *tuiRenderer) progressBar(frac float64, w int) string {
if w < 2 {
return ""
}
filled := int(math.Round(frac * float64(w)))
if filled > w {
filled = w
}
if filled < 0 {
filled = 0
}
col := r.pctColor(frac * 100.0)
return col + strings.Repeat("\u2588", filled) + colDim + strings.Repeat("\u2591", w-filled) + colReset
}

// ---- Color helpers ----

func (r *tuiRenderer) pctColor(pct float64) string {
switch {
case pct >= 85.0:
return r.tc.red
case pct >= 60.0:
return r.tc.yellow
default:
return r.tc.green
}
}

// pctColorInv returns color for values where high is good (e.g. battery charge).
func (r *tuiRenderer) pctColorInv(pct float64) string {
	switch {
	case pct <= 15.0:
		return r.tc.red
	case pct <= 30.0:
		return r.tc.yellow
	default:
		return r.tc.green
	}
}

func (r *tuiRenderer) thermalColor(temp float64) string {
switch {
case temp >= 80.0:
return r.tc.red
case temp >= 65.0:
return r.tc.yellow
default:
return r.tc.green
}
}

// ---- Pure formatting helpers ----

func fmtBytes(b uint64) string {
const (
gib = 1024 * 1024 * 1024
mib = 1024 * 1024
kib = 1024
)
switch {
case b >= gib:
return fmt.Sprintf("%.2f GiB", float64(b)/gib)
case b >= mib:
return fmt.Sprintf("%.2f MiB", float64(b)/mib)
case b >= kib:
return fmt.Sprintf("%.2f KiB", float64(b)/kib)
default:
return fmt.Sprintf("%d B", b)
}
}

// fmtKBps formats a KB/s value as a human-readable rate string.
func fmtKBps(kbps float64) string {
	switch {
	case kbps >= 1024*1024:
		return fmt.Sprintf("%.1f GB/s", kbps/1024/1024)
	case kbps >= 1024:
		return fmt.Sprintf("%.1f MB/s", kbps/1024)
	default:
		return fmt.Sprintf("%.0f KB/s", kbps)
	}
}

func visibleLen(s string) int {
n := 0
inEscape := false
for _, ch := range s {
if ch == '\x1b' {
inEscape = true
continue
}
if inEscape {
if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
inEscape = false
}
continue
}
n++
}
return n
}

// terminalWidth returns the current terminal width, clamped to [40, 220].
// Falls back to 80 when stdout is not a TTY (pipe, test, non-interactive).
func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 40 {
		return 80
	}
	if w > 220 {
		return 220
	}
	return w
}

// HideCursor and ShowCursor are called by main for watch mode.
func HideCursor() {
	fmt.Fprint(os.Stdout, cursorHide)
}

func ShowCursor() {
	fmt.Fprint(os.Stdout, cursorShow)
}
