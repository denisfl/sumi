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
	"sync"
	"time"

	"sumi/internal/model"
)

// New returns the darwin collector.
func New() Collector {
	return &darwinCollector{}
}

// diskIOSample holds a single cumulative read/write counter snapshot.
type diskIOSample struct {
	bytes uint64
	at    time.Time
}

type darwinCollector struct {
	mu          sync.Mutex
	diskReadPrev  map[string]diskIOSample // keyed by device name (e.g. "disk0")
	diskWritePrev map[string]diskIOSample
}

func (c *darwinCollector) Collect(ctx context.Context) (model.Snapshot, error) {
	s := model.Snapshot{
		Platform:  "darwin",
		Timestamp: time.Now(),
		Procs:     []model.ProcEntry{},
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.CPU = collectCPU(ctx)
		s.Thermal.TempC = s.CPU.TempC
		s.Thermal.Sensors = darwinThermalSensors(ctx, s.CPU.TempC)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Net = collectNet(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Try Nvidia first, then Apple Silicon.
		if gpu := collectNvidiaGPU(ctx); gpu != nil {
			s.GPU = gpu
		} else if gpu := collectAppleGPU(ctx); gpu != nil {
			s.GPU = gpu
		}
	}()

	s.Mem = collectMem(ctx)
	s.Disks = collectDisks(ctx)
	c.applyDiskIO(ctx, s.Disks)
	s.Procs = collectProcs(ctx)
	s.Battery = collectBatteryDarwin(ctx)
	s.Hostname, _ = os.Hostname()
	s.Uptime = collectUptimeDarwin(ctx)

	wg.Wait()

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

	// CPU usage: two-sample delta via top -l 2 (runs in parallel with net collector).
	// top -l 2 -n 0 outputs two frames with the default 1-second interval;
	// we take CPU idle% from the second frame for accurate recent-usage delta.
	if out, err := runCmd(ctx, "top", "-l", "2", "-n", "0"); err == nil {
		idle := parseCPUIdleFromTop(out)
		if idle >= 0 {
			cpu.Usage = 100.0 - idle
		}
	}

	// CPU temperature — try osx-cpu-temp (Intel) then smctemp (Intel + Apple Silicon).
	// Each wrapped with a 2-second deadline to avoid hanging Collect.
	// osx-cpu-temp output: "28.8°C" or "CPU: 28.8°C"
	// smctemp output:      "CPU die temperature: 47.0 °C"
	tctx, tcancel := context.WithTimeout(ctx, 2*time.Second)
	defer tcancel()
	if out, err := runCmd(tctx, "osx-cpu-temp"); err == nil {
		if t := parseDegreesC(out); t > 0 {
			cpu.TempC = t
		}
	}
	if cpu.TempC == 0 {
		tctx2, tcancel2 := context.WithTimeout(ctx, 2*time.Second)
		defer tcancel2()
		// smctemp -c prints only a plain float, e.g. "47.9"
		if out, err := runCmd(tctx2, "smctemp", "-c"); err == nil {
			if t, err2 := strconv.ParseFloat(strings.TrimSpace(out), 64); err2 == nil && t > 0 {
				cpu.TempC = t
			}
		}
	}

	return cpu
}

// darwinThermalSensors builds the named sensor list: CPU, GPU (smctemp -g),
// and SSD (smartctl without sudo — silently skipped if unavailable or auth fails).
func darwinThermalSensors(ctx context.Context, cpuTempC float64) []model.ThermalSensor {
	var sensors []model.ThermalSensor

	if cpuTempC > 0 {
		sensors = append(sensors, model.ThermalSensor{Name: "CPU", TempC: cpuTempC})
	}

	// GPU temperature via smctemp -g (Apple Silicon + Intel).
	tctx, tcancel := context.WithTimeout(ctx, 2*time.Second)
	defer tcancel()
	if out, err := runCmd(tctx, "smctemp", "-g"); err == nil {
		if t, err2 := strconv.ParseFloat(strings.TrimSpace(out), 64); err2 == nil && t > 0 {
			sensors = append(sensors, model.ThermalSensor{Name: "GPU", TempC: t})
		}
	}

	// SSD/NVMe temperature via smartctl (requires smartmontools; may need sudo — silently skip).
	// Try /dev/disk0 first (primary internal NVMe on macOS).
	diskTempC := darwinDiskTemp(ctx)
	if diskTempC > 0 {
		sensors = append(sensors, model.ThermalSensor{Name: "SSD", TempC: diskTempC})
	}

	return sensors
}

// darwinDiskTemp tries to read NVMe/SSD temperature via smartctl without root.
// Returns 0 if unavailable or access is denied.
func darwinDiskTemp(ctx context.Context) float64 {
	tctx, tcancel := context.WithTimeout(ctx, 3*time.Second)
	defer tcancel()
	// --nocheck=standby avoids spinning up sleeping drives.
	out, err := runCmd(tctx, "smartctl", "-A", "/dev/disk0", "--nocheck=standby")
	if err != nil {
		return 0
	}
	return parseSmartctlTemp(out)
}

// parseSmartctlTemp extracts temperature from smartctl -A output.
// Handles SATA ("Temperature_Celsius" column) and NVMe ("Temperature:" line) formats.
func parseSmartctlTemp(out string) float64 {
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		// NVMe format: "Temperature:                        34 Celsius"
		if strings.HasPrefix(line, "Temperature:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if t, err := strconv.ParseFloat(fields[1], 64); err == nil && t > 0 {
					return t
				}
			}
		}
		// SATA format: "190 Airflow_Temperature_Cel ... 27" (value is last field)
		if strings.Contains(line, "Temperature_Celsius") || strings.Contains(line, "Temperature_Cel") {
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				if t, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil && t > 0 {
					return t
				}
			}
		}
	}
	return 0
}

// parseCPUIdleFromTop extracts idle% from the last "CPU usage:" line in top -l 2 output.
// Format: "CPU usage: 6.66% user, 13.33% sys, 80.0% idle"
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

// parseDegreesC extracts the first float before a '°' or 'C' character.
// Handles formats: "28.8°C", "CPU: 28.8°C", "CPU die temperature : 45.55 °C".
func parseDegreesC(s string) float64 {
	for i, ch := range s {
		if ch == '°' || (ch == 'C' && i > 0) {
			// Skip spaces between number and degree symbol.
			j := i - 1
			for j >= 0 && s[j] == ' ' {
				j--
			}
			// Walk back through the numeric part.
			end := j + 1
			for j >= 0 && (s[j] == '.' || (s[j] >= '0' && s[j] <= '9')) {
				j--
			}
			numStr := strings.TrimSpace(s[j+1 : end])
			if v, err := strconv.ParseFloat(numStr, 64); err == nil {
				return v
			}
		}
	}
	return 0
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

// applyDiskIO annotates each DiskInfo with per-second KB read/write
// by parsing `iostat -d` cumulative totals and diffing against the previous sample.
// Device-to-mount mapping uses the device field from `df` output (already known from DiskInfo.FSType / name).
// We use `iostat -d -c 1` which prints a header + one data row per disk device.
func (c *darwinCollector) applyDiskIO(ctx context.Context, disks []model.DiskInfo) {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// iostat -d -c 1: columns are device-name groups repeated:
	//   "          disk0              disk1"
	//   "    KB/t  tps  MB/s     KB/t  tps  MB/s"
	//   "  XXXXX  YYY  ZZZ.ZZ  ..."
	// We use -K to get KB/t in KB (not 512-byte units), but MB read/written are cumulative.
	// iostat -I -d -c 1 gives cumulative MB read/written.
	out, err := runCmd(tctx, "iostat", "-I", "-d", "-c", "1")
	if err != nil {
		return
	}
	readKB, writeKB := parseIostatI(out)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.diskReadPrev == nil {
		// First sample — just store and return (no delta yet)
		c.diskReadPrev = make(map[string]diskIOSample)
		c.diskWritePrev = make(map[string]diskIOSample)
		for dev, kb := range readKB {
			c.diskReadPrev[dev] = diskIOSample{bytes: kb * 1024, at: now}
		}
		for dev, kb := range writeKB {
			c.diskWritePrev[dev] = diskIOSample{bytes: kb * 1024, at: now}
		}
		return
	}

	// Build delta rates and annotate disks.
	// macOS mount points come from df -k; the device is the first column (e.g. /dev/disk3s1).
	// We normalise device name to the base disk name (disk0, disk1...) for iostat matching.
	for i := range disks {
		// Extract base disk from FSType field which carries the raw device path.
		dev := baseDiskName(disks[i].FSType)
		rKB, rOK := readKB[dev]
		wKB, wOK := writeKB[dev]
		if !rOK && !wOK {
			continue
		}
		prevR := c.diskReadPrev[dev]
		prevW := c.diskWritePrev[dev]
		elapsed := now.Sub(prevR.at).Seconds()
		if elapsed <= 0 {
			continue
		}
		if rOK && rKB*1024 >= prevR.bytes {
			disks[i].ReadKBps = float64(rKB*1024-prevR.bytes) / 1024.0 / elapsed
		}
		if wOK && wKB*1024 >= prevW.bytes {
			disks[i].WriteKBps = float64(wKB*1024-prevW.bytes) / 1024.0 / elapsed
		}
		c.diskReadPrev[dev] = diskIOSample{bytes: rKB * 1024, at: now}
		c.diskWritePrev[dev] = diskIOSample{bytes: wKB * 1024, at: now}
	}
}

// parseIostatI parses `iostat -I -d -c 1` output and returns cumulative KB read/written
// per device name. iostat -I prints:
//
//	         disk0
//	    KB/t  xfrs    MB
//	  256.00    42  10.5
//
// With multiple devices the device names span the first header row and stats repeat.
func parseIostatI(out string) (readKB, writeKB map[string]uint64) {
	readKB = make(map[string]uint64)
	writeKB = make(map[string]uint64)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return
	}
	// Line 0: device names, padded in groups of 3 columns (15 chars each group).
	// Line 1: column headers repeated per device.
	// Line 2+: data rows.
	// Each device block = 3 fields: KB/t  xfrs  MB  (when -I is used: KB/t tps MB_read MB_write)
	// Actually iostat -I -d on macOS produces 4 columns per device: KB/t xfrs MBrd MBwt
	deviceLine := lines[0]
	dataLine := ""
	for _, l := range lines[2:] {
		l = strings.TrimSpace(l)
		if l != "" {
			dataLine = l
			break
		}
	}
	if dataLine == "" {
		return
	}
	// Extract device names from the header line (each device takes ~20 chars).
	// Use a simpler split: fields in the data line correspond positionally.
	devNames := parseIostatDeviceNames(deviceLine)
	dataFields := strings.Fields(dataLine)
	// Each device has 4 fields: KB/t xfrs MB_read MB_write
	const colsPerDev = 4
	for i, dev := range devNames {
		offset := i * colsPerDev
		if offset+colsPerDev > len(dataFields) {
			break
		}
		// MB_read and MB_write are floats
		mbRead, _ := strconv.ParseFloat(dataFields[offset+2], 64)
		mbWrite, _ := strconv.ParseFloat(dataFields[offset+3], 64)
		readKB[dev] = uint64(mbRead * 1024)
		writeKB[dev] = uint64(mbWrite * 1024)
	}
	return
}

// parseIostatDeviceNames extracts device names from the iostat header line.
// Example: "          disk0              disk1"
func parseIostatDeviceNames(line string) []string {
	return strings.Fields(line)
}

// baseDiskName extracts the base disk identifier from a macOS device path.
// "/dev/disk3s5" -> "disk3", "disk0" -> "disk0".
func baseDiskName(s string) string {
	s = strings.TrimPrefix(s, "/dev/")
	// Strip partition suffix: disk3s5 -> disk3
	for i := len(s) - 1; i > 0; i-- {
		if s[i] == 's' && i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
			return s[:i]
		}
	}
	return s
}

// collectDisks reads df output and returns physical mount points (virtual FSes excluded).
func collectDisks(ctx context.Context) []model.DiskInfo {
	out, err := runCmd(ctx, "df", "-k")
	if err != nil {
		return nil
	}
	// Skip header line; subsequent lines:  Filesystem  1K-blocks  Used  Available  Cap%  Mount
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var disks []model.DiskInfo
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		fs := fields[0]
		mount := fields[len(fields)-1]
		// Exclude pseudo/virtual filesystems.
		if isVirtualFS(fs, mount) {
			continue
		}
		total, _ := strconv.ParseUint(fields[1], 10, 64)
		used, _ := strconv.ParseUint(fields[2], 10, 64)
		avail, _ := strconv.ParseUint(fields[3], 10, 64)
		if total == 0 {
			continue
		}
		disks = append(disks, model.DiskInfo{
			MountPoint: mount,
			FSType:     fs,
			TotalBytes: total * 1024,
			UsedBytes:  used * 1024,
			FreeBytes:  avail * 1024,
		})
	}
	return disks
}

// isVirtualFS returns true for pseudo-filesystems that should not appear in the Disk card.
func isVirtualFS(fs, mount string) bool {
	// macOS virtual filesystem types and paths.
	virtualPrefixes := []string{"devfs", "autofs", "map ", "map:", "tmpfs"}
	for _, pfx := range virtualPrefixes {
		if strings.HasPrefix(fs, pfx) {
			return true
		}
	}
	virtualMounts := []string{"/dev", "/private/var/vm", "/System/Volumes/VM",
		"/System/Volumes/Preboot", "/System/Volumes/Update", "/System/Volumes/xarts",
		"/System/Volumes/iSCPreboot", "/System/Volumes/Hardware"}
	for _, m := range virtualMounts {
		if mount == m {
			return true
		}
	}
	// Exclude any /proc, /sys, /run style mounts.
	virtualDirs := []string{"/proc", "/sys", "/run"}
	for _, d := range virtualDirs {
		if mount == d || strings.HasPrefix(mount, d+"/") {
			return true
		}
	}
	return false
}

// collectNet samples rx/tx bytes on the primary interface over 1 second.
func collectNet(ctx context.Context) model.Net {
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

	rx0, tx0 := netstatBytes(ctx, iface)
	time.Sleep(1 * time.Second)
	rx1, tx1 := netstatBytes(ctx, iface)

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
// Uses ctx (with a 3-second deadline) so a hung netstat cannot block the collector.
// netstat -bI output columns: Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll Drop
func netstatBytes(ctx context.Context, iface string) (rx, tx uint64) {
	nctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := runCmd(nctx, "netstat", "-bI", iface)
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
		pid    int
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
		pidVal, _ := strconv.Atoi(f[1])
		cpuPct, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			continue
		}
		memPct, _ := strconv.ParseFloat(f[3], 64)
		name := f[10]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		rows = append(rows, raw{name, pidVal, cpuPct, memPct})
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
			PID:    r.pid,
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

// collectBatteryDarwin reads battery status via pmset -g batt.
// Returns nil when no battery is present (desktop Mac).
// pmset -g batt output example:
//
//	Now drawing from 'Battery Power'
//	-InternalBattery-0 (id=4653155);	87%; discharging; 2:14 remaining present: true
func collectBatteryDarwin(ctx context.Context) *model.BatteryInfo {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := runCmd(tctx, "pmset", "-g", "batt")
	if err != nil {
		return nil
	}
	return parsePmsetBatt(out)
}

// parsePmsetBatt parses pmset -g batt output into BatteryInfo.
func parsePmsetBatt(out string) *model.BatteryInfo {
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		// Look for the battery data line containing a percentage.
		if !strings.Contains(line, "InternalBattery") && !strings.Contains(line, "id=") {
			continue
		}
		// e.g. "-InternalBattery-0 (id=4653155);	87%; discharging; 2:14 remaining present: true"
		b := &model.BatteryInfo{}
		// Parse charge percentage
		for _, field := range strings.Fields(line) {
			field = strings.Trim(field, ";")
			if strings.HasSuffix(field, "%") {
				pct, err := strconv.ParseFloat(strings.TrimSuffix(field, "%"), 64)
				if err == nil {
					b.ChargePct = pct
				}
			}
		}
		if b.ChargePct == 0 {
			return nil // no battery data parsed
		}
		// Charging state: "charging", "discharging", "charged", "finishing charge"
		if strings.Contains(line, "charging") && !strings.Contains(line, "discharging") {
			b.Charging = true
		}
		// Time remaining: "2:14 remaining" or "charged" / "not charging"
		if b.Charging || b.ChargePct >= 99 {
			b.TimeLeft = "Charging"
			if b.ChargePct >= 99 {
				b.TimeLeft = "Charged"
			}
		} else {
			// Find "H:MM remaining" pattern
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "remaining" && i > 0 {
					hm := strings.Trim(parts[i-1], ";")
					// Convert H:MM to human form
					if hh := strings.Split(hm, ":"); len(hh) == 2 {
						h, _ := strconv.Atoi(hh[0])
						m, _ := strconv.Atoi(hh[1])
						if h > 0 {
							b.TimeLeft = fmt.Sprintf("%dh %dm", h, m)
						} else {
							b.TimeLeft = fmt.Sprintf("%dm", m)
						}
					}
					break
				}
			}
		}
		return b
	}
	return nil
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
