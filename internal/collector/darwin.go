//go:build darwin

// internal/collector/darwin.go
package collector

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"sumi/internal/model"
)

// New returns the darwin collector.
func New() Collector {
	return &darwinCollector{}
}

type darwinCollector struct{}

func (c *darwinCollector) Collect(ctx context.Context) (model.Snapshot, error) {
	s := model.Snapshot{
		Platform:  "darwin",
		Timestamp: time.Now(),
		Procs:     []model.ProcEntry{},
	}

	s.CPU = collectCPU(ctx)
	s.Mem = collectMem(ctx)
	s.Disk = collectDisk(ctx)
	s.Net = collectNet(ctx)
	s.Procs = collectProcs(ctx)
	s.Hostname, _ = os.Hostname()
	s.Uptime = collectUptimeDarwin(ctx)

	return s, nil
}

// collectCPU gathers CPU usage, core count, and model string on macOS.
// Usage is estimated via a 1-second iostat(1) sample.
func collectCPU(ctx context.Context) model.CPU {
	cpu := model.CPU{}

	// Core count
	if out, err := runCmd(ctx, "sysctl", "-n", "hw.logicalcpu"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
			cpu.Cores = n
		}
	}

	// CPU model
	if out, err := runCmd(ctx, "sysctl", "-n", "machdep.cpu.brand_string"); err == nil {
		cpu.Model = strings.TrimSpace(out)
	}

	// CPU usage: sample /dev/null via top -l 2 in non-interactive mode.
	// top -l 2 -n 0 outputs two frames; we take CPU idle from the second.
	if out, err := runCmd(ctx, "top", "-l", "2", "-n", "0", "-s", "1"); err == nil {
		idle := parseCPUIdleFromTop(out)
		if idle >= 0 {
			cpu.Usage = 100.0 - idle
		}
	}

	// CPU temperature via osx-cpu-temp (optional third-party tool).
	// Output is typically "28.8°C" or "CPU: 28.8°C".
	if out, err := runCmd(ctx, "osx-cpu-temp"); err == nil {
		if t := parseDegreesC(out); t > 0 {
			cpu.TempC = t
		}
	}

	return cpu
}

// parseDegreesC extracts the first float preceding a '°' or 'C' character.
func parseDegreesC(s string) float64 {
	// Find the index of '°' or 'C' and try to parse the number before it.
	for i, ch := range s {
		if ch == '°' || (ch == 'C' && i > 0) {
			// Walk back to start of the number
			j := i - 1
			for j >= 0 && (s[j] == '.' || (s[j] >= '0' && s[j] <= '9')) {
				j--
			}
			numStr := strings.TrimSpace(s[j+1 : i])
			if v, err := strconv.ParseFloat(numStr, 64); err == nil {
				return v
			}
		}
	}
	return 0
}

// parseCPUIdleFromTop extracts idle% from the last "CPU usage:" line in top output.
func parseCPUIdleFromTop(out string) float64 {
	var lastLine string
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CPU usage:") {
			lastLine = line
		}
	}
	if lastLine == "" {
		return -1
	}
	// Format: "CPU usage: 6.66% user, 13.33% sys, 80.0% idle"
	for _, part := range strings.Split(lastLine, ",") {
		part = strings.TrimSpace(part)
		if strings.HasSuffix(part, "% idle") {
			s := strings.TrimSuffix(part, "% idle")
			s = strings.TrimSpace(s)
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				return v
			}
		}
	}
	return -1
}

// collectMem parses vm_stat output and sysctl for macOS memory metrics.
func collectMem(_ context.Context) model.Mem {
	m := model.Mem{}

	// Total physical memory
	if out, err := runCmd(context.Background(), "sysctl", "-n", "hw.memsize"); err == nil {
		if n, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64); err == nil {
			m.TotalBytes = n
		}
	}

	// vm_stat gives page counts; page size is typically 4096 or 16384
	pageSize := uint64(4096)
	if out, err := runCmd(context.Background(), "sysctl", "-n", "hw.pagesize"); err == nil {
		if n, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64); err == nil {
			pageSize = n
		}
	}

	if out, err := runCmd(context.Background(), "vm_stat"); err == nil {
		pages := parseVmStat(out)
		free := pages["Pages free"] * pageSize
		inactive := pages["Pages inactive"] * pageSize

		m.FreeBytes = free
		used := m.TotalBytes - free - inactive
		if used > m.TotalBytes {
			used = 0
		}
		m.UsedBytes = used
	}

	// Swap: sysctl vm.swapusage
	if out, err := runCmd(context.Background(), "sysctl", "-n", "vm.swapusage"); err == nil {
		// "total = 1024.00M  used = 0.00M  free = 1024.00M  (encrypted)"
		m.SwapTotal, m.SwapUsed = parseSwapUsage(out)
	}

	return m
}

func parseVmStat(out string) map[string]uint64 {
	result := map[string]uint64{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "."))
		if n, err := strconv.ParseUint(val, 10, 64); err == nil {
			result[key] = n
		}
	}
	return result
}

func parseSwapUsage(out string) (total, used uint64) {
	// "total = 1024.00M  used = 256.00M  free = 768.00M"
	// Simple kv scan
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		for _, seg := range strings.Split(line, "  ") {
			seg = strings.TrimSpace(seg)
			kv := strings.SplitN(seg, "=", 2)
			if len(kv) != 2 {
				continue
			}
			key := strings.TrimSpace(kv[0])
			val := strings.TrimSpace(kv[1])
			bytes := parseSizeToBytes(val)
			switch key {
			case "total":
				total = bytes
			case "used":
				used = bytes
			}
		}
	}
	return total, used
}

// parseSizeToBytes converts "1024.00M", "2.00G", "512.00K" to bytes.
func parseSizeToBytes(s string) uint64 {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0
	}
	suffix := string(s[len(s)-1])
	num := s[:len(s)-1]
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(suffix) {
	case "K":
		return uint64(v * 1024)
	case "M":
		return uint64(v * 1024 * 1024)
	case "G":
		return uint64(v * 1024 * 1024 * 1024)
	}
	return uint64(v)
}

// collectDisk reads df output for the root mount point.
func collectDisk(_ context.Context) model.Disk {
	d := model.Disk{MountPoint: "/"}
	out, err := runCmd(context.Background(), "df", "-k", "/")
	if err != nil {
		return d
	}
	// Skip header; second line has the numbers.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return d
	}
	fields := strings.Fields(lines[1])
	// Filesystem  1K-blocks  Used  Available  Cap%  Mount
	if len(fields) < 6 {
		return d
	}
	// df -k gives 1024-byte blocks
	total, _ := strconv.ParseUint(fields[1], 10, 64)
	used, _ := strconv.ParseUint(fields[2], 10, 64)
	avail, _ := strconv.ParseUint(fields[3], 10, 64)
	d.TotalBytes = total * 1024
	d.UsedBytes = used * 1024
	d.FreeBytes = avail * 1024
	return d
}

// collectNet samples rx/tx bytes on the primary interface over 1 second.
func collectNet(_ context.Context) model.Net {
	n := model.Net{}

	iface := primaryInterface()
	if iface == "" {
		return n
	}
	n.Interface = iface

	// Get IP
	if ifaces, err := net.Interfaces(); err == nil {
		for _, i := range ifaces {
			if i.Name == iface {
				if addrs, err := i.Addrs(); err == nil {
					for _, a := range addrs {
						if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil {
							n.IP = ip.String()
							break
						}
					}
				}
				break
			}
		}
	}

	rx0, tx0 := netstatBytes(iface)
	time.Sleep(1 * time.Second)
	rx1, tx1 := netstatBytes(iface)

	if rx1 >= rx0 {
		n.RxKBps = float64(rx1-rx0) / 1024.0
	}
	if tx1 >= tx0 {
		n.TxKBps = float64(tx1-tx0) / 1024.0
	}
	return n
}

// primaryInterface returns the name of the first non-loopback, up interface
// that has at least one IPv4 address.
func primaryInterface() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	// Prefer en0/en1 with an IPv4 address (Wi-Fi/Ethernet) on macOS
	for _, prefix := range []string{"en0", "en1", "en"} {
		for _, i := range ifaces {
			if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
				continue
			}
			if !strings.HasPrefix(i.Name, prefix) {
				continue
			}
			if hasIPv4(i) {
				return i.Name
			}
		}
	}
	// Fallback: any non-loopback up interface with IPv4
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback == 0 && i.Flags&net.FlagUp != 0 && hasIPv4(i) {
			return i.Name
		}
	}
	return ""
}

// hasIPv4 returns true if the interface has a non-loopback IPv4 address.
func hasIPv4(i net.Interface) bool {
	addrs, err := i.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil && !ip.IsLoopback() {
			return true
		}
	}
	return false
}

// netstatBytes extracts ibytes and obytes for iface via netstat -bI <iface>.
// netstat -bI output columns: Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll Drop
func netstatBytes(iface string) (rx, tx uint64) {
	out, err := runCmd(context.Background(), "netstat", "-bI", iface)
	if err != nil {
		return 0, 0
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Scan() // skip header
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		// First field is Name (may have trailing * for promiscuous mode)
		name := strings.TrimSuffix(fields[0], "*")
		if name != iface {
			continue
		}
		rx, _ = strconv.ParseUint(fields[6], 10, 64)
		tx, _ = strconv.ParseUint(fields[9], 10, 64)
		return rx, tx
	}
	return 0, 0
}

// collectProcs returns top 5 processes by CPU usage.
func collectProcs(_ context.Context) []model.ProcEntry {
	procs := []model.ProcEntry{}
	out, err := runCmd(context.Background(), "ps", "aux")
	if err != nil {
		return procs
	}
	type raw struct {
		name   string
		cpuPct float64
		memPct float64
	}
	var rows []raw
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Scan() // skip header
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		// USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND
		if len(f) < 11 {
			continue
		}
		cpuPct, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			continue
		}
		memPct, _ := strconv.ParseFloat(f[3], 64)
		name := f[10]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		rows = append(rows, raw{name, cpuPct, memPct})
	}
	// Sort by cpuPct descending (simple insertion sort, small n)
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].cpuPct > rows[j-1].cpuPct; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	limit := 5
	if len(rows) < limit {
		limit = len(rows)
	}
	for _, r := range rows[:limit] {
		procs = append(procs, model.ProcEntry{
			Name:   r.name,
			CPUPct: r.cpuPct,
			MemPct: r.memPct,
		})
	}
	return procs
}

// collectUptimeDarwin reads kern.boottime and returns a human-readable uptime string.
func collectUptimeDarwin(ctx context.Context) string {
	out, err := runCmd(ctx, "sysctl", "-n", "kern.boottime")
	if err != nil {
		return ""
	}
	// Format: { sec = 1742000000, usec = 0 } Thu Mar 27 12:00:00 2025
	for _, part := range strings.Split(out, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "{ ")
		if strings.HasPrefix(part, "sec = ") {
			secStr := strings.TrimSpace(strings.TrimPrefix(part, "sec = "))
			if sec, err := strconv.ParseInt(secStr, 10, 64); err == nil {
				return fmtDuration(time.Since(time.Unix(sec, 0)))
			}
		}
	}
	return ""
}

// fmtDuration formats a duration as "Xd HH:MM:SS" or "HH:MM:SS".
func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %02d:%02d:%02d", days, h, m, s)
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// runCmd executes a command and returns its combined output as a string.
// Errors from the command itself (non-zero exit) are returned as err.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s: %w", name, err)
	}
	return out.String(), nil
}
