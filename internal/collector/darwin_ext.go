//go:build darwin

// internal/collector/darwin_ext.go — Extended metrics (v0.7) for macOS.
package collector

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"sumi/internal/model"
)

// ---- Extended Net (macOS) ----

// darwinNetExt collects extended network metrics for macOS.
func darwinNetExt(ctx context.Context, iface string, prev *darwinNetExtState) model.Net {
	n := model.Net{
		PacketLossPct: -1,
		LatencyMs:     -1,
	}

	// OpenConnections: netstat -n | grep ESTABLISHED | wc -l (simplified: count ESTABLISHED lines)
	nctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if out, err := runCmd(nctx, "netstat", "-n"); err == nil {
		count := 0
		scanner := bufio.NewScanner(strings.NewReader(out))
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "ESTABLISHED") {
				count++
			}
		}
		n.OpenConnections = count
	}

	// Default gateway via `route -n get default`
	if gw := darwinDefaultGateway(ctx); gw != "" {
		loss, latency := pingGatewayDarwin(ctx, gw)
		n.PacketLossPct = loss
		n.LatencyMs = latency
	}

	// RxErrors/TxErrors from RouteRIB (already enumerated in collectNet — here we just
	// need per-interface error counters). We read them separately to avoid coupling.
	if rx, tx, ok := darwinIfaceErrors(iface); ok {
		if prev.netDevValid {
			if rx >= prev.rxErrors {
				n.RxErrors = rx - prev.rxErrors
			}
			if tx >= prev.txErrors {
				n.TxErrors = tx - prev.txErrors
			}
		}
		prev.rxErrors = rx
		prev.txErrors = tx
		prev.netDevValid = true
	}

	return n
}

// darwinDefaultGateway returns the IPv4 default gateway from `route -n get default`.
func darwinDefaultGateway(ctx context.Context) string {
	gctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := runCmd(gctx, "route", "-n", "get", "default")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

// parsePingStatsDarwin parses macOS ping output using the shared parsePingStats from ext_parsers.go.
func pingGatewayDarwin(ctx context.Context, gw string) (loss, latency float64) {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runCmd(pctx, "ping", "-c", "3", "-W", "1000", gw)
	if err != nil && out == "" {
		return -1, -1
	}
	return parsePingStats(out)
}

// darwinIfaceErrors reads cumulative error counters for an interface using
// the same NET_RT_IFLIST2 sysctl path already used for rx/tx bytes.
func darwinIfaceErrors(iface string) (rxErrors, txErrors uint64, ok bool) {
	// We use netstat -bI <iface> as it's simpler than re-parsing sysctl for errors.
	nctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := runCmd(nctx, "netstat", "-bI", iface)
	if err != nil {
		return 0, 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Scan() // skip header
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		name := strings.TrimSuffix(fields[0], "*")
		if name != iface {
			continue
		}
		// columns: Name Mtu Net Addr Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll Drop
		rxErrors, _ = strconv.ParseUint(fields[5], 10, 64)
		txErrors, _ = strconv.ParseUint(fields[8], 10, 64)
		return rxErrors, txErrors, true
	}
	return 0, 0, false
}

// ---- Extended Disk (macOS) ----

// darwinInodesPct annotates each DiskInfo with InodesUsedPct via `df -i`.
func darwinInodesPct(ctx context.Context, disks []model.DiskInfo) {
	out, err := runCmd(ctx, "df", "-i")
	if err != nil {
		return
	}
	// parseDfInodes is defined in ext_parsers.go (shared, no build tag).
	inodeMap := parseDfInodes(out)
	for i := range disks {
		if pct, ok := inodeMap[disks[i].MountPoint]; ok {
			disks[i].InodesUsedPct = pct
		}
	}
}

// darwinSmartStatus runs `smartctl -H <dev>` for each disk device.
func darwinSmartStatus(ctx context.Context, disks []model.DiskInfo) {
	// Map mount points to disk device names (disk0, disk1, ...) via `df`.
	for i := range disks {
		dev := darwinDiskDevForMount(disks[i].MountPoint)
		if dev == "" {
			continue
		}
		disks[i].SmartStatus = runSmartHealthDarwin(ctx, "/dev/"+dev)
	}
}

// darwinDiskDevForMount returns the base disk device (e.g. "disk1") for a mount point.
func darwinDiskDevForMount(mount string) string {
	out, err := exec.Command("df", mount).Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return ""
	}
	fields := strings.Fields(lines[1])
	if len(fields) == 0 {
		return ""
	}
	return baseDiskName(fields[0])
}

// runSmartHealthDarwin runs `smartctl -H <dev>` and returns "ok", "warn", "fail", or "".
func runSmartHealthDarwin(ctx context.Context, dev string) string {
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runCmd(tctx, "smartctl", "-H", dev)
	if err != nil && out == "" {
		return ""
	}
	// parseSmartHealth is defined in ext_parsers.go (shared, no build tag).
	return parseSmartHealth(out)
}

// ---- SystemLoad (macOS) ----

// darwinSystemLoad collects system-load metrics on macOS.
func darwinSystemLoad(ctx context.Context, prev *darwinSysLoadState) model.SystemLoad {
	sl := model.SystemLoad{}

	// Load averages via `sysctl vm.loadavg` → "{ 0.52 0.58 0.59 }"
	if out, err := runCmd(ctx, "sysctl", "-n", "vm.loadavg"); err == nil {
		out = strings.Trim(strings.TrimSpace(out), "{} ")
		fields := strings.Fields(out)
		if len(fields) >= 3 {
			sl.Load1, _ = strconv.ParseFloat(fields[0], 64)
			sl.Load5, _ = strconv.ParseFloat(fields[1], 64)
			sl.Load15, _ = strconv.ParseFloat(fields[2], 64)
		}
	}

	// Uptime from kern.boottime → "{ sec = 1742000000, usec = 0 } ..."
	if out, err := runCmd(ctx, "sysctl", "-n", "kern.boottime"); err == nil {
		for _, part := range strings.Split(out, ",") {
			part = strings.TrimSpace(part)
			part = strings.TrimPrefix(part, "{ ")
			if strings.HasPrefix(part, "sec = ") {
				secStr := strings.TrimSpace(strings.TrimPrefix(part, "sec = "))
				if sec, err := strconv.ParseInt(secStr, 10, 64); err == nil {
					sl.UptimeSeconds = uint64(time.Since(time.Unix(sec, 0)).Seconds())
				}
			}
		}
	}

	// FdUsedPct via sysctl kern.num_files / kern.maxfiles
	if numOut, err1 := runCmd(ctx, "sysctl", "-n", "kern.num_files"); err1 == nil {
		if maxOut, err2 := runCmd(ctx, "sysctl", "-n", "kern.maxfiles"); err2 == nil {
			num, _ := strconv.ParseUint(strings.TrimSpace(numOut), 10, 64)
			max, _ := strconv.ParseUint(strings.TrimSpace(maxOut), 10, 64)
			if max > 0 {
				sl.FdUsedPct = float64(num) / float64(max) * 100.0
			}
		}
	}

	// Zombie processes: ps axo stat= | count lines starting with Z
	if out, err := runCmd(ctx, "ps", "axo", "stat="); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(out))
		for scanner.Scan() {
			if strings.HasPrefix(strings.TrimSpace(scanner.Text()), "Z") {
				sl.ZombieProcs++
			}
		}
	}

	// Context switches: last line of `vmstat 1 2`
	// vmstat output cols: ... cs ...
	tctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if out, err := runCmd(tctx, "vmstat", "1", "2"); err == nil {
		now := time.Now()
		cs := parseVmstatCS(out)
		if cs > 0 {
			if prev.ctxtValid {
				elapsed := now.Sub(prev.ctxtTime).Seconds()
				if elapsed > 0 && cs >= prev.ctxtTotal {
					sl.ContextSwitchesPerSec = float64(cs-prev.ctxtTotal) / elapsed
				}
			}
			prev.ctxtTotal = cs
			prev.ctxtTime = now
			prev.ctxtValid = true
		}
	}

	return sl
}

// parseVmstatCS extracts cumulative context switches from vmstat output.
// Returns the value from the last data line.
func parseVmstatCS(out string) uint64 {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0
	}
	// Find header line containing "cs"
	var csIdx int = -1
	for _, line := range lines {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "cs" {
				csIdx = i
				break
			}
		}
		if csIdx >= 0 {
			break
		}
	}
	if csIdx < 0 {
		return 0
	}
	// Get last non-empty data line
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if csIdx < len(fields) {
			if v, err := strconv.ParseUint(fields[csIdx], 10, 64); err == nil {
				return v
			}
		}
	}
	return 0
}

// ---- State structs ----

type darwinNetExtState struct {
	rxErrors    uint64
	txErrors    uint64
	netDevValid bool
}

type darwinSysLoadState struct {
	ctxtTotal uint64
	ctxtTime  time.Time
	ctxtValid bool
}

// ---- darwinExtCollector ----

// darwinExtCollector wraps darwinCollector and adds v0.7 extended state.
type darwinExtCollector struct {
	*darwinCollector
	extMu   sync.Mutex
	netExt  darwinNetExtState
	sysLoad darwinSysLoadState
}

var _ Collector = (*darwinExtCollector)(nil)

// Collect delegates to the embedded darwinCollector then enriches the snapshot.
func (c *darwinExtCollector) Collect(ctx context.Context) (model.Snapshot, error) {
	snap, err := c.darwinCollector.Collect(ctx)
	if err != nil {
		return snap, err
	}

	c.extMu.Lock()
	ext := darwinNetExt(ctx, snap.Net.Interface, &c.netExt)
	c.extMu.Unlock()

	snap.Net.OpenConnections = ext.OpenConnections
	snap.Net.PacketLossPct = ext.PacketLossPct
	snap.Net.LatencyMs = ext.LatencyMs
	snap.Net.RxErrors = ext.RxErrors
	snap.Net.TxErrors = ext.TxErrors

	darwinInodesPct(ctx, snap.Disks)
	darwinSmartStatus(ctx, snap.Disks)

	c.extMu.Lock()
	snap.SystemLoad = darwinSystemLoad(ctx, &c.sysLoad)
	c.extMu.Unlock()

	snap.WireGuard = collectWireGuard(ctx)

	return snap, nil
}
