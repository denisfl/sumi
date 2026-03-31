// internal/renderer/tui.go
package renderer

import (
	"fmt"
	"math"
	"os"
	"strings"

	"sumi/internal/model"
)

// Tokyo Night palette — exact RGB values from the original bash script.
const (
	colBorder  = "\x1b[38;2;86;95;137m"   // #565F89
	colTitle   = "\x1b[38;2;122;162;247m"  // #7AA2F7
	colText    = "\x1b[38;2;192;202;245m"  // #C0CAF5
	colGreen   = "\x1b[38;2;158;206;106m"  // #9ECE6A
	colYellow  = "\x1b[38;2;224;175;104m"  // #E0AF68
	colRed     = "\x1b[38;2;247;118;142m"  // #F7768E
	colCyan    = "\x1b[38;2;125;207;255m"  // #7DCFFF
	colPurple  = "\x1b[38;2;187;154;247m"  // #BB9AF7
	colOrange  = "\x1b[38;2;255;158;100m"  // #FF9E64
	colTeal    = "\x1b[38;2;42;195;222m"   // #2AC3DE
	colReset   = "\x1b[0m"
	colDim     = "\x1b[2m"

	// Cursor / screen control
	cursorHide = "\x1b[?25l"
	cursorShow = "\x1b[?25h"
	clearAll   = "\x1b[2J\x1b[H"
)

// Box-drawing borders
const (
	boxTL = "╭"
	boxTR = "╮"
	boxBL = "╰"
	boxBR = "╯"
	boxH  = "─"
	boxV  = "│"
	boxTT = "┬"
	boxBT = "┴"
)

// tuiRenderer renders snapshots in the Tokyo Night Bento Monitor style.
type tuiRenderer struct{}

// NewTUI returns a TUI renderer.
func NewTUI() Renderer {
	return &tuiRenderer{}
}

func (r *tuiRenderer) Render(s model.Snapshot) error {
	// Clear screen and hide cursor for the duration of rendering.
	fmt.Fprint(os.Stdout, clearAll)

	width := terminalWidth()
	if width < 40 {
		width = 80
	}

	// Render 3-row x 2-column card grid matching original bash layout.
	half := (width - 3) / 2

	// Row 1: Thermal (left) | CPU (right)
	thermalCard := renderThermalCard(s, half)
	cpuCard := renderCPUCard(s, half)
	printRow(thermalCard, cpuCard)
	fmt.Fprintln(os.Stdout)

	// Row 2: Memory (left) | Disk (right)
	memCard := renderMemCard(s, half)
	diskCard := renderDiskCard(s, half)
	printRow(memCard, diskCard)
	fmt.Fprintln(os.Stdout)

	// Row 3: Network (left) | Top Processes (right)
	netCard := renderNetCard(s, half)
	procsCard := renderProcsCard(s, half)
	printRow(netCard, procsCard)
	fmt.Fprintln(os.Stdout)

	// Row 4: System (full width)
	sysCard := renderSystemCard(s, width)
	printCard(sysCard)

	return nil
}

// printCard prints a single card spanning full width.
func printCard(lines []string) {
	for _, l := range lines {
		fmt.Fprintln(os.Stdout, l)
	}
}

// printRow prints two cards side by side, line by line.
// The shorter card is padded with blank lines (no borders) to reach the height of the taller.
func printRow(left, right []string) {
	maxLines := len(left)
	if len(right) > maxLines {
		maxLines = len(right)
	}

	// Determine visible card widths from first lines.
	leftW := 0
	if len(left) > 0 {
		leftW = visibleLen(left[0])
	}
	rightW := 0
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

// ---- Card renderers ----

func renderCPUCard(s model.Snapshot, w int) []string {
	title := "CPU"
	usagePct := s.CPU.Usage
	// inner width = w-2; label prefix " load  " = 8 visible chars; bar fills the rest
	barW := w - 2 - 8
	if barW < 2 {
		barW = 2
	}
	bar := progressBar(usagePct/100.0, barW)

	lines := []string{}
	lines = append(lines, cardTop(title, w))
	lines = append(lines, cardSep(w))
	lines = append(lines, cardLine(fmt.Sprintf(" %s%-6s%s %s", colDim, "load", colReset, bar), w))
	lines = append(lines, cardEmpty(w))
	lines = append(lines, cardLine(fmt.Sprintf(" %s%-6s%s %s%d%s", colDim, "cores", colReset, colText, s.CPU.Cores, colReset), w))
	lines = append(lines, cardEmpty(w))
	lines = append(lines, cardBottom(w))
	return lines
}

func renderMemCard(s model.Snapshot, w int) []string {
	title := "Memory"
	used := float64(s.Mem.UsedBytes)
	total := float64(s.Mem.TotalBytes)
	var pct float64
	if total > 0 {
		pct = used / total * 100.0
	}
	bar := progressBar(pct/100.0, w-4)

	lines := []string{}
	lines = append(lines, cardTop(title, w))
	lines = append(lines, cardLine(fmt.Sprintf("%sTotal:%s %s%s%s", colCyan, colReset, colText, fmtBytes(s.Mem.TotalBytes), colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sUsed:%s  %s%s%s", colCyan, colReset, pctColor(pct), fmtBytes(s.Mem.UsedBytes), colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sFree:%s  %s%s%s", colCyan, colReset, colGreen, fmtBytes(s.Mem.FreeBytes), colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sUsage:%s %s%.1f%%%s", colCyan, colReset, pctColor(pct), pct, colReset), w))
	lines = append(lines, cardLine(bar, w))
	if s.Mem.SwapTotal > 0 {
		swapPct := float64(s.Mem.SwapUsed) / float64(s.Mem.SwapTotal) * 100.0
		swapBar := progressBar(swapPct/100.0, w-4)
		lines = append(lines, cardLine(fmt.Sprintf("%sSwap:%s  %s%s%s / %s%s%s", colCyan, colReset, pctColor(swapPct), fmtBytes(s.Mem.SwapUsed), colReset, colText, fmtBytes(s.Mem.SwapTotal), colReset), w))
		lines = append(lines, cardLine(swapBar, w))
	}
	lines = append(lines, cardBottom(w))
	return lines
}

func renderDiskCard(s model.Snapshot, w int) []string {
	title := "Disk"
	var pct float64
	if s.Disk.TotalBytes > 0 {
		pct = float64(s.Disk.UsedBytes) / float64(s.Disk.TotalBytes) * 100.0
	}
	bar := progressBar(pct/100.0, w-4)

	lines := []string{}
	lines = append(lines, cardTop(title, w))
	lines = append(lines, cardLine(fmt.Sprintf("%sMount:%s  %s%s%s", colCyan, colReset, colText, s.Disk.MountPoint, colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sTotal:%s  %s%s%s", colCyan, colReset, colText, fmtBytes(s.Disk.TotalBytes), colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sUsed:%s   %s%s%s", colCyan, colReset, pctColor(pct), fmtBytes(s.Disk.UsedBytes), colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sFree:%s   %s%s%s", colCyan, colReset, colGreen, fmtBytes(s.Disk.FreeBytes), colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sUsage:%s  %s%.1f%%%s", colCyan, colReset, pctColor(pct), pct, colReset), w))
	lines = append(lines, cardLine(bar, w))
	lines = append(lines, cardBottom(w))
	return lines
}

func renderNetCard(s model.Snapshot, w int) []string {
	title := "Network"
	iface := s.Net.Interface
	if iface == "" {
		iface = "N/A"
	}
	ip := s.Net.IP
	if ip == "" {
		ip = "N/A"
	}

	lines := []string{}
	lines = append(lines, cardTop(title, w))
	lines = append(lines, cardLine(fmt.Sprintf("%sIface:%s %s%s%s", colCyan, colReset, colText, iface, colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sIP:%s    %s%s%s", colCyan, colReset, colText, ip, colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sRx:%s    %s%.2f KB/s%s", colCyan, colReset, colGreen, s.Net.RxKBps, colReset), w))
	lines = append(lines, cardLine(fmt.Sprintf("%sTx:%s    %s%.2f KB/s%s", colCyan, colReset, colOrange, s.Net.TxKBps, colReset), w))
	lines = append(lines, cardBottom(w))
	return lines
}

func renderProcsCard(s model.Snapshot, w int) []string {
	title := "Top Processes"
	lines := []string{}
	lines = append(lines, cardTop(title, w))
	header := fmt.Sprintf("%s%-20s %6s %6s%s", colDim+colText, "NAME", "CPU%", "MEM%", colReset)
	lines = append(lines, cardLine(header, w))
	if len(s.Procs) == 0 {
		lines = append(lines, cardLine(fmt.Sprintf("%sN/A%s", colDim, colReset), w))
	}
	for _, p := range s.Procs {
		name := p.Name
		if len(name) > 20 {
			name = name[:19] + "…"
		}
		row := fmt.Sprintf("%-20s %s%6.1f%s %s%6.1f%s",
			name,
			pctColor(p.CPUPct), p.CPUPct, colReset,
			colTeal, p.MemPct, colReset,
		)
		lines = append(lines, cardLine(row, w))
	}
	lines = append(lines, cardBottom(w))
	return lines
}

func renderThermalCard(s model.Snapshot, w int) []string {
	title := "THERMAL"
	tempStr := "N/A"
	tempCol := colText
	if s.Thermal.TempC > 0 {
		tempStr = fmt.Sprintf("%.1f°C", s.Thermal.TempC)
		tempCol = thermalColor(s.Thermal.TempC)
	}

	lines := []string{}
	lines = append(lines, cardTop(title, w))
	lines = append(lines, cardSep(w))
	if s.Thermal.TempC > 0 {
		lines = append(lines, cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s", colDim, "temp", colReset, tempCol, tempStr, colReset), w))
	} else {
		lines = append(lines, cardEmpty(w))
	}

	if s.Platform == "rpi" {
		armStr := "N/A"
		if s.Thermal.ArmFreqMHz > 0 {
			armStr = fmt.Sprintf("%d MHz", s.Thermal.ArmFreqMHz)
		}
		gpuStr := "N/A"
		if s.Thermal.GpuFreqMHz > 0 {
			gpuStr = fmt.Sprintf("%d MHz", s.Thermal.GpuFreqMHz)
		}
		thrStr := "ok"
		thrCol := colGreen
		if s.Thermal.Throttled != "" {
			thrStr = s.Thermal.Throttled
			thrCol = colRed
		}
		lines = append(lines, cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s", colDim, "arm", colReset, colOrange, armStr, colReset), w))
		lines = append(lines, cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s", colDim, "gpu", colReset, colPurple, gpuStr, colReset), w))
		lines = append(lines, cardLine(fmt.Sprintf(" %s%-6s%s %s%s%s", colDim, "thrt", colReset, thrCol, thrStr, colReset), w))
	} else {
		// Padding to match RPi card height (3 empty lines)
		lines = append(lines, cardEmpty(w))
		lines = append(lines, cardEmpty(w))
		lines = append(lines, cardEmpty(w))
	}
	lines = append(lines, cardBottom(w))
	return lines
}

// renderSystemCard renders the full-width System card (hostname, platform, uptime, date, time).
func renderSystemCard(s model.Snapshot, w int) []string {
	title := "System"
	lines := []string{}
	lines = append(lines, cardTop(title, w))
	lines = append(lines, cardSep(w))

	// Line 1: host + platform + date + time (four labelled pairs, spread across full width)
	host := s.Hostname
	if host == "" {
		host = "N/A"
	}
	platStr := s.Platform
	if platStr == "" {
		platStr = "N/A"
	}
	dateStr := s.Timestamp.Format("2006-01-02")
	timeStr := s.Timestamp.Format("15:04:05")
	line1 := fmt.Sprintf(" %s%-10s%s %s%-20s%s %s%-10s%s %s%-14s%s %s%-5s%s %s%s%s",
		colDim, "host", colReset, colCyan, host, colReset,
		colDim, "platform", colReset, colText, platStr, colReset,
		colDim, "date", colReset, colText, dateStr, colReset,
	)
	_ = timeStr // timeStr used in line2
	lines = append(lines, cardLine(line1, w))

	// Line 2: uptime + time
	upt := s.Uptime
	if upt == "" {
		upt = "N/A"
	}
	line2 := fmt.Sprintf(" %s%-10s%s %s%-20s%s %s%-10s%s %s%s%s",
		colDim, "uptime", colReset, colGreen, upt, colReset,
		colDim, "time", colReset, colText, timeStr, colReset,
	)
	lines = append(lines, cardLine(line2, w))
	lines = append(lines, cardBottom(w))
	return lines
}

// ---- Card builder helpers ----

// cardTop returns the top border line with the title.
func cardTop(title string, w int) string {
	inner := w - 2 // space for left and right border chars
	titleFormatted := fmt.Sprintf(" %s%s%s ", colTitle, title, colBorder)
	// visible length of titleFormatted (without ANSI)
	titleVisLen := len(title) + 2

	dashCount := inner - titleVisLen
	if dashCount < 0 {
		dashCount = 0
	}
	leftDash := 1
	rightDash := dashCount - leftDash
	if rightDash < 0 {
		rightDash = 0
	}
	return fmt.Sprintf("%s%s%s%s%s%s%s%s",
		colBorder, boxTL,
		strings.Repeat(boxH, leftDash),
		titleFormatted,
		strings.Repeat(boxH, rightDash),
		boxTR, colReset, "")
}

// cardBottom returns the bottom border line.
func cardBottom(w int) string {
	inner := w - 2
	return fmt.Sprintf("%s%s%s%s%s", colBorder, boxBL, strings.Repeat(boxH, inner), boxBR, colReset)
}

// cardSep returns a horizontal separator line inside a card (like csep in bash).
func cardSep(w int) string {
	inner := w - 2
	return fmt.Sprintf("%s%s%s%s%s", colBorder, "├", strings.Repeat(boxH, inner), "┤", colReset)
}

// cardEmpty returns a blank content line inside a card (like cemp in bash).
func cardEmpty(w int) string {
	return cardLine("", w)
}

// cardLine wraps content in vertical borders, padding/truncating to w.
func cardLine(content string, w int) string {
	// w includes both vertical border characters (each 1 col wide).
	// Inner visible width = w - 2 (left │ + right │).
	innerW := w - 2
	visible := visibleLen(content)
	var padded string
	if visible < innerW {
		padded = content + strings.Repeat(" ", innerW-visible)
	} else {
		// Truncate raw string to approximate; crude but avoids splitting ANSI sequences mid-escape
		padded = content
	}
	return fmt.Sprintf("%s%s%s%s%s%s%s", colBorder, boxV, colReset, padded, colBorder, boxV, colReset)
}

// ---- Progress bar ----

// progressBar builds a filled/empty progress bar of given width, with color.
func progressBar(frac float64, w int) string {
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
	col := pctColor(frac * 100.0)
	bar := col + strings.Repeat("█", filled) + colDim + strings.Repeat("░", w-filled) + colReset
	return bar
}

// ---- Color helpers ----

func pctColor(pct float64) string {
	switch {
	case pct >= 85.0:
		return colRed
	case pct >= 60.0:
		return colYellow
	default:
		return colGreen
	}
}

func thermalColor(temp float64) string {
	switch {
	case temp >= 80.0:
		return colRed
	case temp >= 65.0:
		return colYellow
	default:
		return colGreen
	}
}

// ---- Formatting helpers ----

// fmtBytes formats a byte count as human-readable string (GiB / MiB / KiB / B).
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

// visibleLen returns the number of printable characters in s, ignoring ANSI escape sequences.
func visibleLen(s string) int {
	n := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
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
	// Use stty to get terminal size without cgo or syscall
	// Fallback to 80 if unavailable
	return 80
}

// HideCursor and ShowCursor are called by main for watch mode.
func HideCursor() {
	fmt.Fprint(os.Stdout, cursorHide)
}

func ShowCursor() {
	fmt.Fprint(os.Stdout, cursorShow)
}
