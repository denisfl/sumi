// internal/renderer/tui.go
package renderer

import (
"fmt"
"math"
"os"
"strings"

"sumi/internal/config"
"sumi/internal/model"
"sumi/internal/theme"
)

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
compact bool
tc      themeColors
box     theme.BoxChars
}

// NewTUI returns a TUI renderer with the given config, theme, and border style.
func NewTUI(cfg config.Config, t theme.Theme, bc theme.BoxChars) Renderer {
return &tuiRenderer{
compact: cfg.CompactMode,
tc:      newThemeColors(t),
box:     bc,
}
}

func (r *tuiRenderer) Render(s model.Snapshot) error {
fmt.Fprint(os.Stdout, clearAll)
width := terminalWidth()
if width < 40 {
width = 80
}
if r.compact {
return r.renderCompact(s, width)
}
return r.renderFull(s, width)
}

func (r *tuiRenderer) renderFull(s model.Snapshot, width int) error {
half := (width - 3) / 2
printRow(r.renderThermalCard(s, half), r.renderCPUCard(s, half))
fmt.Fprintln(os.Stdout)
printRow(r.renderMemCard(s, half), r.renderDiskCard(s, half))
fmt.Fprintln(os.Stdout)
printRow(r.renderNetCard(s, half), r.renderProcsCard(s, half))
fmt.Fprintln(os.Stdout)
printCard(r.renderSystemCard(s, width))
return nil
}

func (r *tuiRenderer) renderCompact(s model.Snapshot, width int) error {
half := (width - 3) / 2
printRow(
r.renderCompactCard("THERMAL", r.compactThermalLine(s), 0, half),
r.renderCompactCard("CPU", r.compactCPULine(s), s.CPU.Usage, half),
)
fmt.Fprintln(os.Stdout)
memPct := 0.0
if s.Mem.TotalBytes > 0 {
memPct = float64(s.Mem.UsedBytes) / float64(s.Mem.TotalBytes) * 100.0
}
diskPct := 0.0
if s.Disk.TotalBytes > 0 {
diskPct = float64(s.Disk.UsedBytes) / float64(s.Disk.TotalBytes) * 100.0
}
printRow(
r.renderCompactCard("MEM", r.compactMemLine(s, memPct), memPct, half),
r.renderCompactCard("DISK", r.compactDiskLine(s, diskPct), diskPct, half),
)
fmt.Fprintln(os.Stdout)
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
return fmt.Sprintf(" %s%-4s%s %s%s%s  %s%.1f%%%s",
colDim, "mnt", colReset, r.tc.text, s.Disk.MountPoint, colReset,
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
fmt.Fprintln(os.Stdout, l)
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
fmt.Fprintf(os.Stdout, "%s %s\n", l, r)
}
}

// ---- Full-mode card renderers ----

func (r *tuiRenderer) renderCPUCard(s model.Snapshot, w int) []string {
barW := w - 2 - 8
if barW < 2 {
barW = 2
}
bar := r.progressBar(s.CPU.Usage/100.0, barW)
lines := []string{r.cardTop("CPU", w), r.cardSep(w)}
lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s", colDim, "load", colReset, bar), w))
if len(s.CPU.CoreUsages) > 0 {
blocks := []string{" ", "\u2581", "\u2582", "\u2583", "\u2584", "\u2585", "\u2586", "\u2587", "\u2588"}
micro := ""
for _, u := range s.CPU.CoreUsages {
idx := int(u / 100.0 * 8.0)
if idx > 8 {
idx = 8
}
micro += blocks[idx]
}
lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s", colDim, "cores", colReset, r.pctColor(s.CPU.Usage), micro, colReset), w))
} else {
lines = append(lines, r.cardEmpty(w))
lines = append(lines, r.cardLine(fmt.Sprintf(" %s%-6s%s %s%d%s", colDim, "cores", colReset, r.tc.text, s.CPU.Cores, colReset), w))
}
lines = append(lines, r.cardEmpty(w))
lines = append(lines, r.cardBottom(w))
return lines
}

func (r *tuiRenderer) renderMemCard(s model.Snapshot, w int) []string {
var pct float64
if s.Mem.TotalBytes > 0 {
pct = float64(s.Mem.UsedBytes) / float64(s.Mem.TotalBytes) * 100.0
}
bar := r.progressBar(pct/100.0, w-4)
lines := []string{r.cardTop("Memory", w)}
lines = append(lines, r.cardLine(fmt.Sprintf("%sTotal:%s %s%s%s", r.tc.cyan, colReset, r.tc.text, fmtBytes(s.Mem.TotalBytes), colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sUsed:%s  %s%s%s", r.tc.cyan, colReset, r.pctColor(pct), fmtBytes(s.Mem.UsedBytes), colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sFree:%s  %s%s%s", r.tc.cyan, colReset, r.tc.green, fmtBytes(s.Mem.FreeBytes), colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sUsage:%s %s%.1f%%%s", r.tc.cyan, colReset, r.pctColor(pct), pct, colReset), w))
lines = append(lines, r.cardLine(bar, w))
if s.Mem.SwapTotal > 0 {
swapPct := float64(s.Mem.SwapUsed) / float64(s.Mem.SwapTotal) * 100.0
swapBar := r.progressBar(swapPct/100.0, w-4)
lines = append(lines, r.cardLine(fmt.Sprintf("%sSwap:%s  %s%s%s / %s%s%s",
r.tc.cyan, colReset, r.pctColor(swapPct), fmtBytes(s.Mem.SwapUsed), colReset,
r.tc.text, fmtBytes(s.Mem.SwapTotal), colReset), w))
lines = append(lines, r.cardLine(swapBar, w))
}
lines = append(lines, r.cardBottom(w))
return lines
}

func (r *tuiRenderer) renderDiskCard(s model.Snapshot, w int) []string {
var pct float64
if s.Disk.TotalBytes > 0 {
pct = float64(s.Disk.UsedBytes) / float64(s.Disk.TotalBytes) * 100.0
}
bar := r.progressBar(pct/100.0, w-4)
lines := []string{r.cardTop("Disk", w)}
lines = append(lines, r.cardLine(fmt.Sprintf("%sMount:%s  %s%s%s", r.tc.cyan, colReset, r.tc.text, s.Disk.MountPoint, colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sTotal:%s  %s%s%s", r.tc.cyan, colReset, r.tc.text, fmtBytes(s.Disk.TotalBytes), colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sUsed:%s   %s%s%s", r.tc.cyan, colReset, r.pctColor(pct), fmtBytes(s.Disk.UsedBytes), colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sFree:%s   %s%s%s", r.tc.cyan, colReset, r.tc.green, fmtBytes(s.Disk.FreeBytes), colReset), w))
lines = append(lines, r.cardLine(fmt.Sprintf("%sUsage:%s  %s%.1f%%%s", r.tc.cyan, colReset, r.pctColor(pct), pct, colReset), w))
lines = append(lines, r.cardLine(bar, w))
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
lines = append(lines, r.cardLine(fmt.Sprintf("%sTx:%s    %s%.2f KB/s%s", r.tc.cyan, colReset, r.tc.orange, s.Net.TxKBps, colReset), w))
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
if len(name) > 20 {
name = name[:19] + "\u2026"
}
row := fmt.Sprintf("%-20s %s%6.1f%s %s%6.1f%s",
name, r.pctColor(p.CPUPct), p.CPUPct, colReset,
r.tc.teal, p.MemPct, colReset)
lines = append(lines, r.cardLine(row, w))
}
lines = append(lines, r.cardBottom(w))
return lines
}

func (r *tuiRenderer) renderThermalCard(s model.Snapshot, w int) []string {
	lines := []string{r.cardTop("THERMAL", w), r.cardSep(w)}

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

lines := []string{r.cardTop("System", w), r.cardSep(w)}
line1 := fmt.Sprintf(" %s%-10s%s %s%-20s%s %s%-10s%s %s%-14s%s %s%-5s%s %s%s%s",
colDim, "host", colReset, r.tc.cyan, host, colReset,
colDim, "platform", colReset, r.tc.text, platStr, colReset,
colDim, "date", colReset, r.tc.text, dateStr, colReset)
lines = append(lines, r.cardLine(line1, w))
line2 := fmt.Sprintf(" %s%-10s%s %s%-20s%s %s%-10s%s %s%s%s",
colDim, "uptime", colReset, r.tc.green, upt, colReset,
colDim, "time", colReset, r.tc.text, timeStr, colReset)
lines = append(lines, r.cardLine(line2, w))
lines = append(lines, r.cardBottom(w))
return lines
}

// ---- Card border helpers ----

func (r *tuiRenderer) cardTop(title string, w int) string {
inner := w - 2
titleFormatted := fmt.Sprintf(" %s%s%s ", r.tc.title, title, r.tc.border)
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
r.tc.border, r.box.TL,
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

// terminalWidth returns the current terminal width, or 80 as fallback.
func terminalWidth() int {
return 80
}

// HideCursor and ShowCursor are called by main for watch mode.
func HideCursor() {
fmt.Fprint(os.Stdout, cursorHide)
}

func ShowCursor() {
fmt.Fprint(os.Stdout, cursorShow)
}
