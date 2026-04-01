//go:build linux

// internal/collector/linux.go
package collector

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sumi/internal/model"
)

// New returns the linux collector (or Raspberry Pi collector when applicable).
func New() Collector {
	if isRaspberryPi() {
		return &rpiCollector{base: &linuxCollector{}}
	}
	return &linuxCollector{}
}

type linuxCollector struct{}

func (c *linuxCollector) Collect(ctx context.Context) (model.Snapshot, error) {
	s := model.Snapshot{
		Platform:  "linux",
		Timestamp: time.Now(),
		Procs:     []model.ProcEntry{},
	}
	s.CPU = linuxCPU(ctx)
	s.Mem = linuxMem()
	s.Disk = linuxDisk(ctx)
	s.Net = linuxNet()
	s.Procs = linuxProcs(ctx)
	s.Thermal.TempC = linuxThermal()
	s.Hostname, _ = os.Hostname()
	s.Uptime = linuxUptime()
	return s, nil
}

// linuxUptime reads /proc/uptime and returns a human-readable string.
func linuxUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return ""
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return ""
	}
	d := time.Duration(secs) * time.Second
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

// linuxCPU computes CPU usage by sampling /proc/stat twice over 1 second.
// It also collects per-core usage via a 200ms delta (nested within the 1s window).
func linuxCPU(ctx context.Context) model.CPU {
	cpu := model.CPU{}

	type coreStats struct {
		idle, total uint64
	}

	// readAll reads aggregate and per-core stats from /proc/stat.
	readAll := func() (agg coreStats, cores []coreStats) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			var isCPU bool
			var isAggregate bool
			if strings.HasPrefix(line, "cpu ") {
				isCPU = true
				isAggregate = true
			} else if strings.HasPrefix(line, "cpu") {
				isCPU = true
			}
			if !isCPU {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			vals := make([]uint64, len(fields)-1)
			for i, f := range fields[1:] {
				vals[i], _ = strconv.ParseUint(f, 10, 64)
			}
			idleTime := vals[3]
			if len(vals) > 4 {
				idleTime += vals[4]
			}
			var sum uint64
			for _, v := range vals {
				sum += v
			}
			st := coreStats{idle: idleTime, total: sum}
			if isAggregate {
				agg = st
			} else {
				cores = append(cores, st)
			}
		}
		return
	}

	agg0, cores0 := readAll()
	time.Sleep(200 * time.Millisecond)
	agg1, cores1 := readAll()
	// Wait the remaining ~800ms for the full 1s aggregate sample
	time.Sleep(800 * time.Millisecond)
	agg2, _ := readAll()

	dTotal := agg2.total - agg0.total
	dIdle := agg2.idle - agg0.idle
	if dTotal > 0 {
		cpu.Usage = (1.0 - float64(dIdle)/float64(dTotal)) * 100.0
	}

	// Per-core usage from the 200ms window
	if len(cores0) > 0 && len(cores1) == len(cores0) {
		cpu.CoreUsages = make([]float64, len(cores0))
		for i := range cores0 {
			dt := cores1[i].total - cores0[i].total
			di := cores1[i].idle - cores0[i].idle
			if dt > 0 {
				cpu.CoreUsages[i] = (1.0 - float64(di)/float64(dt)) * 100.0
			}
		}
	}
	_ = agg1 // silence unused variable warning

	// Core count
	if out, err := runCmd(ctx, "nproc"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
			cpu.Cores = n
		}
	}

	// CPU model
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cpu.Model = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}

	return cpu
}

// linuxMem reads /proc/meminfo.
func linuxMem() model.Mem {
	m := model.Mem{}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return m
	}
	kv := map[string]uint64{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.TrimSuffix(val, " kB")
		if n, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64); err == nil {
			kv[key] = n * 1024
		}
	}
	m.TotalBytes = kv["MemTotal"]
	m.FreeBytes = kv["MemFree"] + kv["Buffers"] + kv["Cached"]
	if m.FreeBytes > m.TotalBytes {
		m.FreeBytes = kv["MemFree"]
	}
	m.UsedBytes = m.TotalBytes - m.FreeBytes
	m.SwapTotal = kv["SwapTotal"]
	m.SwapUsed = kv["SwapTotal"] - kv["SwapFree"]
	return m
}

// linuxDisk reads df output for /.
func linuxDisk(ctx context.Context) model.Disk {
	d := model.Disk{MountPoint: "/"}
	out, err := runCmd(ctx, "df", "-k", "/")
	if err != nil {
		return d
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return d
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return d
	}
	total, _ := strconv.ParseUint(fields[1], 10, 64)
	used, _ := strconv.ParseUint(fields[2], 10, 64)
	avail, _ := strconv.ParseUint(fields[3], 10, 64)
	d.TotalBytes = total * 1024
	d.UsedBytes = used * 1024
	d.FreeBytes = avail * 1024
	return d
}

// linuxNet samples rx/tx counters from /sys/class/net over 1 second.
func linuxNet() model.Net {
	n := model.Net{}
	iface := linuxPrimaryInterface()
	if iface == "" {
		return n
	}
	n.Interface = iface

	if ifaces, err := net.Interfaces(); err == nil {
		for _, i := range ifaces {
			if i.Name != iface {
				continue
			}
			if addrs, err := i.Addrs(); err == nil {
				for _, a := range addrs {
					if ip, _, err := net.ParseCIDR(a.String()); err == nil && ip.To4() != nil {
						n.IP = ip.String()
						break
					}
				}
			}
		}
	}

	rx0 := readSysNetStat(iface, "rx_bytes")
	tx0 := readSysNetStat(iface, "tx_bytes")
	time.Sleep(1 * time.Second)
	rx1 := readSysNetStat(iface, "rx_bytes")
	tx1 := readSysNetStat(iface, "tx_bytes")

	if rx1 >= rx0 {
		n.RxKBps = float64(rx1-rx0) / 1024.0
	}
	if tx1 >= tx0 {
		n.TxKBps = float64(tx1-tx0) / 1024.0
	}
	return n
}

func readSysNetStat(iface, stat string) uint64 {
	path := filepath.Join("/sys/class/net", iface, "statistics", stat)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return n
}

// linuxPrimaryInterface returns the first non-loopback up interface.
func linuxPrimaryInterface() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		return i.Name
	}
	return ""
}

// linuxProcs returns top 5 processes sorted by CPU%.
func linuxProcs(ctx context.Context) []model.ProcEntry {
	procs := []model.ProcEntry{}
	out, err := runCmd(ctx, "ps", "aux", "--sort=-%cpu")
	if err != nil {
		return procs
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Scan() // skip header
	count := 0
	for scanner.Scan() && count < 5 {
		f := strings.Fields(scanner.Text())
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
		procs = append(procs, model.ProcEntry{Name: name, CPUPct: cpuPct, MemPct: memPct})
		count++
	}
	return procs
}

// linuxThermal reads /sys/class/thermal/thermal_zone0/temp (millidegrees C).
func linuxThermal() float64 {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0
	}
	return n / 1000.0
}

// runCmd executes a command and returns stdout+stderr as a string.
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
