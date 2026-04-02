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
	"sort"
	"strconv"
	"strings"
	"sync"
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

type linuxCollector struct {
	mu              sync.Mutex
	diskStatsPrev   map[string]diskstatEntry // keyed by device name (e.g. "sda")
	diskStatsTime   time.Time
	// Disk total cache — TotalBytes only changes when mounts change.
	diskTotalMu    sync.Mutex
	diskMountHash  string            // sorted concatenation of mount points
	diskTotalCache map[string]uint64 // mount point → TotalBytes
	// Static cache — populated once on the first Collect() call.
	cacheOnce sync.Once
	hostname  string
	cpuCores  int
	cpuModel  string
}

func (c *linuxCollector) initCache() {
	c.cacheOnce.Do(func() {
		c.hostname, _ = os.Hostname()
		// Core count via nproc.
		if data, err := os.ReadFile("/sys/devices/system/cpu/present"); err == nil {
			// e.g. "0-3" → 4 cores
			s := strings.TrimSpace(string(data))
			if idx := strings.Index(s, "-"); idx >= 0 {
				if n, err := strconv.Atoi(s[idx+1:]); err == nil {
					c.cpuCores = n + 1
				}
			} else if n, err := strconv.Atoi(s); err == nil {
				c.cpuCores = n + 1
			}
		}
		// CPU model from /proc/cpuinfo "model name" or "Hardware".
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
					if idx := strings.Index(line, ":"); idx >= 0 {
						c.cpuModel = strings.TrimSpace(line[idx+1:])
						break
					}
				}
			}
		}
	})
}

// diskstatEntry holds the cumulative sector counts from /proc/diskstats.
type diskstatEntry struct {
	readiSectors  uint64
	writeSectors uint64
}

func (c *linuxCollector) Collect(ctx context.Context) (model.Snapshot, error) {
	c.initCache()

	s := model.Snapshot{
		Platform:  "linux",
		Timestamp: time.Now(),
		Procs:     []model.ProcEntry{},
	}

	// Group A: slow sources (~1 s each) — run concurrently.
	var cpuResult model.CPU
	var netResult model.Net
	var wgA sync.WaitGroup
	wgA.Add(2)
	go func() {
		defer wgA.Done()
		cpuResult = linuxCPU(ctx)
	}()
	go func() {
		defer wgA.Done()
		netResult = linuxNet()
	}()

	// Group B: fast sources — run concurrently with Group A.
	var memResult model.Mem
	var disksResult []model.DiskInfo
	var procsResult []model.ProcEntry
	var thermalTempC float64
	var thermalSensors []model.ThermalSensor
	var batteryResult *model.BatteryInfo
	var uptime string
	var wgB sync.WaitGroup
	wgB.Add(6)
	go func() { defer wgB.Done(); memResult = linuxMem() }()
	go func() {
		defer wgB.Done()
		disksResult = c.linuxDisksWithCache(ctx)
		c.applyDiskIO(disksResult)
	}()
	go func() { defer wgB.Done(); procsResult = linuxProcs(ctx) }()
	go func() { defer wgB.Done(); thermalTempC = linuxThermal() }()
	go func() { defer wgB.Done(); thermalSensors = linuxThermalSensors() }()
	go func() { defer wgB.Done(); batteryResult = linuxBattery() }()
	uptime = linuxUptime()

	// Group C: optional GPU (may take up to 3 s if tool is absent).
	var gpuResult *model.GPUInfo
	var wgC sync.WaitGroup
	wgC.Add(1)
	go func() {
		defer wgC.Done()
		if gpu := collectNvidiaGPU(ctx); gpu != nil {
			gpuResult = gpu
		} else if gpu := collectAMDGPU(ctx); gpu != nil {
			gpuResult = gpu
		}
	}()

	// Wait for all groups.
	wgA.Wait()
	wgB.Wait()
	wgC.Wait()

	s.CPU = cpuResult
	if c.cpuCores > 0 {
		s.CPU.Cores = c.cpuCores
	}
	if c.cpuModel != "" {
		s.CPU.Model = c.cpuModel
	}
	s.Net = netResult
	s.Mem = memResult
	s.Disks = disksResult
	s.Procs = procsResult
	s.Thermal.TempC = thermalTempC
	s.Thermal.Sensors = thermalSensors
	s.Battery = batteryResult
	s.Hostname = c.hostname
	s.Uptime = uptime
	s.GPU = gpuResult
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
		pid, _ := strconv.Atoi(f[1])
		name := f[10]
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		container := procContainer(pid)
		procs = append(procs, model.ProcEntry{Name: name, PID: pid, CPUPct: cpuPct, MemPct: memPct, Container: container})
		count++
	}
	return procs
}

// procContainer reads /proc/PID/cgroup and returns "docker", "k8s", or "".
func procContainer(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "docker-") || strings.Contains(line, "/docker/") {
			return "docker"
		}
		if strings.Contains(line, "kubepods") {
			return "k8s"
		}
	}
	return ""
}

// linuxDisksWithCache collects disk info. On the first call (or when the set of
// mount points changes) it runs `df` to get TotalBytes and caches them. On
// subsequent calls with an unchanged mount list it runs `df` only for
// UsedBytes/FreeBytes and fills TotalBytes from the cache — avoiding the syscall
// overhead of df's stat on every mount for a value that almost never changes.
func (c *linuxCollector) linuxDisksWithCache(ctx context.Context) []model.DiskInfo {
	disks := linuxDisks(ctx)
	if len(disks) == 0 {
		return disks
	}

	// Build mount-list hash as sorted concatenation of mount points.
	mountPoints := make([]string, len(disks))
	for i, d := range disks {
		mountPoints[i] = d.MountPoint
	}
	sort.Strings(mountPoints)
	hash := strings.Join(mountPoints, "\x00")

	c.diskTotalMu.Lock()
	defer c.diskTotalMu.Unlock()

	if hash != c.diskMountHash || c.diskTotalCache == nil {
		// Mount list changed (or first run): refresh the TotalBytes cache.
		c.diskMountHash = hash
		c.diskTotalCache = make(map[string]uint64, len(disks))
		for _, d := range disks {
			c.diskTotalCache[d.MountPoint] = d.TotalBytes
		}
	} else {
		// Mount list stable: apply cached TotalBytes to avoid a re-stat at the OS level.
		for i := range disks {
			if cached, ok := c.diskTotalCache[disks[i].MountPoint]; ok {
				disks[i].TotalBytes = cached
			}
		}
	}

	return disks
}

// applyDiskIO reads /proc/diskstats, diffs against the previous sample, and
// annotates each DiskInfo.ReadKBps / WriteKBps with the measured throughput.
// /proc/diskstats fields (1-indexed): major minor devname reads_completed ... sectors_read ... writes_completed ... sectors_written ...
// Field offsets (0-based after devname): reads=1, sectors_read=3, writes=5, sectors_written=7
func (c *linuxCollector) applyDiskIO(disks []model.DiskInfo) {
	now := time.Now()
	cur, err := readDiskstats()
	if err != nil {
		return
	}
	// Build mount → device mapping via /proc/mounts.
	mountTodev := linuxMountToDev()

	c.mu.Lock()
	defer c.mu.Unlock()

	prev := c.diskStatsPrev
	elapsed := now.Sub(c.diskStatsTime).Seconds()

	// Store current for next call.
	c.diskStatsPrev = cur
	c.diskStatsTime = now

	if prev == nil || elapsed <= 0 {
		// First sample — no delta yet.
		return
	}

	for i := range disks {
		dev := mountTodev[disks[i].MountPoint]
		if dev == "" {
			continue
		}
		prevE, ok := prev[dev]
		if !ok {
			continue
		}
		curE, ok := cur[dev]
		if !ok {
			continue
		}
		const sectorSize = 512 // Linux always uses 512-byte sectors in diskstats
		if curE.readiSectors >= prevE.readiSectors {
			disks[i].ReadKBps = float64(curE.readiSectors-prevE.readiSectors) * sectorSize / 1024.0 / elapsed
		}
		if curE.writeSectors >= prevE.writeSectors {
			disks[i].WriteKBps = float64(curE.writeSectors-prevE.writeSectors) * sectorSize / 1024.0 / elapsed
		}
	}
}

// readDiskstats parses /proc/diskstats and returns a map of device → sector counts.
func readDiskstats() (map[string]diskstatEntry, error) {
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	result := make(map[string]diskstatEntry)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// /proc/diskstats: major minor name r_cmpl r_mrg r_sect r_ms w_cmpl w_mrg w_sect ...
		if len(fields) < 10 {
			continue
		}
		dev := fields[2]
		rSect, _ := strconv.ParseUint(fields[5], 10, 64)
		wSect, _ := strconv.ParseUint(fields[9], 10, 64)
		result[dev] = diskstatEntry{readiSectors: rSect, writeSectors: wSect}
	}
	return result, nil
}

// linuxMountToDev reads /proc/mounts and returns a map of mount-point → device-name.
// Device names are stripped of /dev/ prefix and partition numbers to match diskstats entries.
// e.g. "/dev/sda1" -> "sda", "/dev/nvme0n1p1" -> "nvme0n1"
func linuxMountToDev() map[string]string {
	result := make(map[string]string)
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return result
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		dev := fields[0]
		mount := fields[1]
		// Only physical devices.
		if !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		devName := strings.TrimPrefix(dev, "/dev/")
		// Strip partition suffix: sda1 -> sda, nvme0n1p1 -> nvme0n1, mmcblk0p1 -> mmcblk0
		devName = stripPartitionSuffix(devName)
		result[mount] = devName
	}
	return result
}

// stripPartitionSuffix returns the base device name without the partition identifier.
// sda1 -> sda, nvme0n1p3 -> nvme0n1, mmcblk0p1 -> mmcblk0, hda2 -> hda
func stripPartitionSuffix(dev string) string {
	// NVMe/MMC style: ends with pN where preceding char is a digit.
	for i := len(dev) - 1; i > 0; i-- {
		if dev[i] >= '0' && dev[i] <= '9' {
			continue
		}
		if dev[i] == 'p' && i > 0 && dev[i-1] >= '0' && dev[i-1] <= '9' {
			return dev[:i]
		}
		break
	}
	// SATA/IDE style: strip trailing digits from device name with mixed alpha/digits.
	i := len(dev) - 1
	for i >= 0 && dev[i] >= '0' && dev[i] <= '9' {
		i--
	}
	// Only strip if remainder is non-empty and ends in a letter (sda1 -> sda, not just "1")
	if i >= 0 && i < len(dev)-1 && dev[i] >= 'a' && dev[i] <= 'z' {
		return dev[:i+1]
	}
	return dev
}

// linuxDisks parses df -B1 -T output and returns non-virtual mount points.
func linuxDisks(ctx context.Context) []model.DiskInfo {
	out, err := runCmd(ctx, "df", "-B1", "-T")
	if err != nil {
		// Fallback: use -k if -T is unsupported (busybox df).
		out, err = runCmd(ctx, "df", "-k")
		if err != nil {
			return nil
		}
		return parseLinuxDfK(out)
	}
	return parseLinuxDfBT(out)
}

// parseLinuxDfBT parses `df -B1 -T` output.
// Columns: Filesystem Type 1B-blocks Used Available Use% Mount
func parseLinuxDfBT(out string) []model.DiskInfo {
	var disks []model.DiskInfo
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		fs := fields[0]
		fsType := fields[1]
		mount := fields[len(fields)-1]
		if isLinuxVirtualFS(fsType, mount) {
			continue
		}
		total, _ := strconv.ParseUint(fields[2], 10, 64)
		used, _ := strconv.ParseUint(fields[3], 10, 64)
		avail, _ := strconv.ParseUint(fields[4], 10, 64)
		if total == 0 {
			continue
		}
		_ = fs
		disks = append(disks, model.DiskInfo{
			MountPoint: mount,
			FSType:     fsType,
			TotalBytes: total,
			UsedBytes:  used,
			FreeBytes:  avail,
		})
	}
	return disks
}

// parseLinuxDfK parses `df -k` output (busybox fallback).
func parseLinuxDfK(out string) []model.DiskInfo {
	var disks []model.DiskInfo
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		fs := fields[0]
		mount := fields[len(fields)-1]
		if isLinuxVirtualFS("", mount) || strings.HasPrefix(fs, "none") {
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
			TotalBytes: total * 1024,
			UsedBytes:  used * 1024,
			FreeBytes:  avail * 1024,
		})
	}
	return disks
}

// isLinuxVirtualFS returns true for pseudo-filesystems.
func isLinuxVirtualFS(fsType, mount string) bool {
	virtualTypes := map[string]bool{
		"tmpfs": true, "devtmpfs": true, "sysfs": true, "proc": true,
		"cgroup": true, "cgroup2": true, "pstore": true, "efivarfs": true,
		"bpf": true, "securityfs": true, "debugfs": true, "tracefs": true,
		"hugetlbfs": true, "mqueue": true, "fusectl": true, "devpts": true,
		"squashfs": true, "overlay": true, "ramfs": true, "none": true,
	}
	if fsType != "" && virtualTypes[fsType] {
		return true
	}
	virtualMounts := []string{"/proc", "/sys", "/run", "/dev", "/boot/efi"}
	for _, m := range virtualMounts {
		if mount == m || strings.HasPrefix(mount, m+"/") {
			return true
		}
	}
	return false
}

// linuxDisk reads df output for / (kept for backward compatibility with rpi.go).
func linuxDisk(ctx context.Context) model.DiskInfo {
	d := model.DiskInfo{MountPoint: "/"}
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

// linuxThermalSensors reads all thermal_zone entries and returns
// named ThermalSensor values. Zone type names are mapped to friendly labels.
func linuxThermalSensors() []model.ThermalSensor {
	var sensors []model.ThermalSensor
	entries, err := os.ReadDir("/sys/class/thermal")
	if err != nil {
		return sensors
	}
	seen := make(map[string]bool)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "thermal_zone") {
			continue
		}
		base := filepath.Join("/sys/class/thermal", name)
		tempData, err := os.ReadFile(filepath.Join(base, "temp"))
		if err != nil || len(tempData) == 0 {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(string(tempData)), 64)
		if err != nil || n <= 0 {
			continue
		}
		tempC := n / 1000.0
		typeData, _ := os.ReadFile(filepath.Join(base, "type"))
		zoneName := strings.TrimSpace(string(typeData))
		if zoneName == "" {
			zoneName = name
		}
		// Map common zone types to friendly display names.
		label := zoneLabel(zoneName)
		if seen[label] {
			continue // deduplicate by label
		}
		seen[label] = true
		sensors = append(sensors, model.ThermalSensor{Name: label, TempC: tempC})
	}
	return sensors
}

// zoneLabel maps a raw thermal_zone type string to a short display name.
func zoneLabel(raw string) string {
	l := strings.ToLower(raw)
	switch {
	case strings.Contains(l, "x86_pkg"), strings.Contains(l, "acpitz"),
		strings.Contains(l, "cpu"), strings.Contains(l, "core"):
		return "CPU"
	case strings.Contains(l, "gpu"):
		return "GPU"
	case strings.Contains(l, "nvme"), strings.Contains(l, "ssd"):
		return "SSD"
	default:
		return raw
	}
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

// linuxBattery reads /sys/class/power_supply/ and returns battery status.
// Returns nil when no battery is present.
func linuxBattery() *model.BatteryInfo {
	// Look for BAT0, BAT1, or any entry with type "Battery".
	entries, err := os.ReadDir("/sys/class/power_supply")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		dir := "/sys/class/power_supply/" + e.Name()
		// Check type == "Battery"
		typData, err := os.ReadFile(dir + "/type")
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(typData)) != "Battery" {
			continue
		}
		b := &model.BatteryInfo{}
		// Charge percentage: try capacity first, then energy_now/energy_full
		if cap, err := readSysInt(dir + "/capacity"); err == nil {
			b.ChargePct = float64(cap)
		} else if now, nerr := readSysUint(dir + "/energy_now"); nerr == nil {
			if full, ferr := readSysUint(dir + "/energy_full"); ferr == nil && full > 0 {
				b.ChargePct = float64(now) / float64(full) * 100.0
			}
		}
		if b.ChargePct == 0 {
			continue
		}
		// Status: "Charging", "Discharging", "Full", "Not charging"
		if status, err := os.ReadFile(dir + "/status"); err == nil {
			st := strings.TrimSpace(string(status))
			switch st {
			case "Charging":
				b.Charging = true
				b.TimeLeft = "Charging"
			case "Full":
				b.TimeLeft = "Charged"
			case "Discharging":
				// Try to compute time remaining from current_now + charge_now
				if currentUA, err := readSysUint(dir + "/current_now"); err == nil && currentUA > 0 {
					if chargeUAh, err := readSysUint(dir + "/charge_now"); err == nil {
						hours := float64(chargeUAh) / float64(currentUA)
						h := int(hours)
						m := int((hours - float64(h)) * 60)
						if h > 0 {
							b.TimeLeft = fmt.Sprintf("%dh %dm", h, m)
						} else {
							b.TimeLeft = fmt.Sprintf("%dm", m)
						}
					}
				}
			default:
				b.TimeLeft = st
			}
		}
		return b
	}
	return nil
}

// readSysInt reads a sysfs file as an integer.
func readSysInt(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// readSysUint reads a sysfs file as a uint64.
func readSysUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
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
