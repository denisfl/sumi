//go:build linux

// internal/collector/linux_ext.go — Extended metrics (v0.7) for Linux.
package collector

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sumi/internal/model"
)

// ---- Extended Net ----

// linuxNetExt collects extended network metrics that complement the base Net struct.
// It receives the already-determined primary interface name.
func linuxNetExt(ctx context.Context, iface string, prev *netExtState) model.Net {
	n := model.Net{
		PacketLossPct: -1,
		LatencyMs:     -1,
	}

	// OpenConnections: parse `ss -s` for established count.
	if out, err := runCmd(ctx, "ss", "-s"); err == nil {
		n.OpenConnections = parseSsEstab(out)
	}

	// Gateway ping for PacketLossPct + LatencyMs.
	if gw := linuxDefaultGateway(ctx); gw != "" {
		loss, latency := pingGateway(ctx, gw)
		n.PacketLossPct = loss
		n.LatencyMs = latency
	}

	// /proc/net/snmp for TcpRetransmits delta.
	if cur, err := readSnmpRetrans(); err == nil {
		if prev.snmpRetransValid {
			if cur >= prev.snmpRetrans {
				n.TcpRetransmits = cur - prev.snmpRetrans
			}
		}
		prev.snmpRetrans = cur
		prev.snmpRetransValid = true
	}

	// /proc/net/dev for RxErrors / TxErrors delta.
	if curRx, curTx, err := readNetDevErrors(iface); err == nil {
		if prev.netDevValid {
			if curRx >= prev.rxErrors {
				n.RxErrors = curRx - prev.rxErrors
			}
			if curTx >= prev.txErrors {
				n.TxErrors = curTx - prev.txErrors
			}
		}
		prev.rxErrors = curRx
		prev.txErrors = curTx
		prev.netDevValid = true
	}

	return n
}

// parseSsEstab is defined in ext_parsers.go (shared, no build tag).

// linuxDefaultGateway returns the IPv4 default gateway from `ip route show default`.
func linuxDefaultGateway(ctx context.Context) string {
	gctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := runCmd(gctx, "ip", "route", "show", "default")
	if err != nil {
		return ""
	}
	// "default via 192.168.1.1 dev eth0 ..."
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "via" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}

// parsePingStats is defined in ext_parsers.go (shared, no build tag)
// pingGateway sends 3 ICMP pings with 1s timeout and returns (lossPercent, avgMs).
// Returns -1, -1 on failure.
func pingGateway(ctx context.Context, gw string) (loss, latency float64) {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runCmd(pctx, "ping", "-c", "3", "-W", "1", gw)
	if err != nil && out == "" {
		return -1, -1
	}
	return parsePingStats(out)
}

// readSnmpRetrans reads the cumulative RetransSegs counter from /proc/net/snmp.
func readSnmpRetrans() (uint64, error) {
	data, err := os.ReadFile("/proc/net/snmp")
	if err != nil {
		return 0, err
	}
	// Two lines per protocol: header then values.
	// "Tcp: ... RetransSegs ..."
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var tcpKeys []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Tcp:") {
			fields := strings.Fields(line)
			if len(tcpKeys) == 0 {
				tcpKeys = fields
			} else {
				// Second Tcp: line = values
				for i, k := range tcpKeys {
					if k == "RetransSegs" && i < len(fields) {
						return strconv.ParseUint(fields[i], 10, 64)
					}
				}
			}
		}
	}
	return 0, nil
}

// readNetDevErrors reads cumulative rx/tx error counts for iface from /proc/net/dev.
// /proc/net/dev columns: iface: rx_bytes rx_packets rx_errs rx_drop rx_fifo rx_frame rx_compressed rx_multicast | tx_bytes...
// errors are index 3 (rx, 0-based after split on colon) and index 11 (tx).
func readNetDevErrors(iface string) (rxErrors, txErrors uint64, err error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) != iface {
			continue
		}
		fields := strings.Fields(parts[1])
		// fields: rx_bytes rx_pkt rx_errs rx_drop ... tx_bytes tx_pkt tx_errs ...
		// indices: 0        1      2        3          8        9      10
		if len(fields) >= 11 {
			rxErrors, _ = strconv.ParseUint(fields[2], 10, 64)
			txErrors, _ = strconv.ParseUint(fields[10], 10, 64)
		}
		return rxErrors, txErrors, nil
	}
	return 0, 0, nil
}

// ---- Extended Disk ----

// parseDfInodes is defined in ext_parsers.go (shared, no build tag).
// linuxInodesPct annotates each DiskInfo with InodesUsedPct via `df -i`.
func linuxInodesPct(ctx context.Context, disks []model.DiskInfo) {
	out, err := runCmd(ctx, "df", "-i")
	if err != nil {
		return
	}
	inodeMap := parseDfInodes(out)
	for i := range disks {
		if pct, ok := inodeMap[disks[i].MountPoint]; ok {
			disks[i].InodesUsedPct = pct
		}
	}
}

// linuxAwaitMs annotates each DiskInfo with AwaitMs from `iostat -x 1 2`.
func linuxAwaitMs(ctx context.Context, disks []model.DiskInfo, mountToDev map[string]string) {
	tctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := runCmd(tctx, "iostat", "-x", "1", "2")
	if err != nil {
		return
	}
	awaitMap := parseIostatX(out)
	for i := range disks {
		dev := mountToDev[disks[i].MountPoint]
		if dev == "" {
			continue
		}
		if v, ok := awaitMap[dev]; ok {
			disks[i].AwaitMs = v
		}
	}
}

// parseIostatX extracts await column from `iostat -x` output.
// Parses the last data section (second sample) for each device.
func parseIostatX(out string) map[string]float64 {
	result := map[string]float64{}
	lines := strings.Split(strings.TrimSpace(out), "\n")

	// Find the last "Device" header line — that starts the second sample section.
	lastHeader := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "Device") {
			lastHeader = i
			break
		}
	}
	if lastHeader < 0 || lastHeader >= len(lines)-1 {
		return result
	}

	// Determine column index of "await" from the header line.
	headerFields := strings.Fields(lines[lastHeader])
	awaitIdx := -1
	for j, h := range headerFields {
		if strings.ToLower(h) == "await" {
			awaitIdx = j
			break
		}
	}
	if awaitIdx < 0 {
		return result
	}

	for _, line := range lines[lastHeader+1:] {
		fields := strings.Fields(line)
		if len(fields) <= awaitIdx {
			continue
		}
		dev := fields[0]
		if v, err := strconv.ParseFloat(fields[awaitIdx], 64); err == nil {
			result[dev] = v
		}
	}
	return result
}

// linuxSmartStatus annotates each DiskInfo with SmartStatus via `smartctl -H`.
func linuxSmartStatus(ctx context.Context, disks []model.DiskInfo, mountToDev map[string]string) {
	for i := range disks {
		dev := mountToDev[disks[i].MountPoint]
		if dev == "" {
			continue
		}
		fullDev := "/dev/" + dev
		disks[i].SmartStatus = runSmartHealth(ctx, fullDev)
	}
}

// parseSmartHealth is defined in ext_parsers.go (shared, no build tag).

// runSmartHealth runs `smartctl -H <dev>` and returns "ok", "warn", "fail", or "" if unavailable.
func runSmartHealth(ctx context.Context, dev string) string {
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runCmd(tctx, "smartctl", "-H", dev)
	if err != nil && out == "" {
		return ""
	}
	return parseSmartHealth(out)
}

// ---- SystemLoad ----

// linuxSystemLoad collects system-load metrics.
func linuxSystemLoad(prev *sysLoadState) model.SystemLoad {
	sl := model.SystemLoad{}

	// Load averages from /proc/loadavg: "0.52 0.58 0.59 3/432 12345"
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			sl.Load1, _ = strconv.ParseFloat(fields[0], 64)
			sl.Load5, _ = strconv.ParseFloat(fields[1], 64)
			sl.Load15, _ = strconv.ParseFloat(fields[2], 64)
		}
	}

	// Uptime in seconds from /proc/uptime: "12345.67 23456.78"
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 1 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				sl.UptimeSeconds = uint64(v)
			}
		}
	}

	// FdUsedPct from /proc/sys/fs/file-nr: "allocated 0 max"
	if data, err := os.ReadFile("/proc/sys/fs/file-nr"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			allocated, _ := strconv.ParseUint(fields[0], 10, 64)
			maxFd, _ := strconv.ParseUint(fields[2], 10, 64)
			if maxFd > 0 {
				sl.FdUsedPct = float64(allocated) / float64(maxFd) * 100.0
			}
		}
	}

	// Zombie processes: count entries with "State:\tZ" in /proc/[pid]/status
	sl.ZombieProcs = countZombieProcs()

	// Context switches per second from /proc/stat "ctxt" line.
	if cur, err := readCtxtTotal(); err == nil {
		now := time.Now()
		if prev.ctxtValid {
			elapsed := now.Sub(prev.ctxtTime).Seconds()
			if elapsed > 0 && cur >= prev.ctxtTotal {
				sl.ContextSwitchesPerSec = float64(cur-prev.ctxtTotal) / elapsed
			}
		}
		prev.ctxtTotal = cur
		prev.ctxtTime = now
		prev.ctxtValid = true
	}

	return sl
}

// countZombieProcs counts processes in state Z by scanning /proc/*/status.
func countZombieProcs() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only numeric directories (PIDs)
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		statusPath := filepath.Join("/proc", e.Name(), "status")
		data, err := os.ReadFile(statusPath)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "State:") {
				if strings.Contains(line, "Z") {
					count++
				}
				break
			}
		}
	}
	return count
}

// readCtxtTotal reads the cumulative context switch count from /proc/stat.
func readCtxtTotal() (uint64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ctxt ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strconv.ParseUint(fields[1], 10, 64)
			}
		}
	}
	return 0, nil
}

// ---- SystemEvent ----

// linuxCollectEvents collects OS-level events since the given time.
// Returns an empty slice (never nil) on failure. This is Linux-only.
func linuxCollectEvents(ctx context.Context, since time.Time, prev *eventState) []model.SystemEvent {
	var events []model.SystemEvent
	events = append(events, linuxDmesgEvents(ctx, since)...)
	events = append(events, linuxSshEvents(ctx, since)...)
	events = append(events, linuxServiceRestartEvents(ctx, since)...)
	// Reboot detection via uptime decrease (handled externally by passing current UptimeSeconds).
	_ = prev
	return events
}

// linuxDmesgEvents scans dmesg for oom_kill and disk_error events.
func linuxDmesgEvents(ctx context.Context, since time.Time) []model.SystemEvent {
	var events []model.SystemEvent
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Use --since to limit output; requires util-linux dmesg >= 2.23
	sinceStr := since.Format("2006-01-02T15:04:05")
	out, err := runCmd(tctx, "dmesg", "--since", sinceStr, "--time-format=iso")
	if err != nil {
		// Fallback: plain dmesg (may return more output but still parseable)
		out, _ = runCmd(tctx, "dmesg")
		if out == "" {
			return events
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		lower := strings.ToLower(line)
		if strings.Contains(lower, "oom") && strings.Contains(lower, "kill") {
			detail := extractOOMProcess(line)
			events = append(events, model.SystemEvent{Ts: time.Now(), Kind: "oom_kill", Detail: detail})
		} else if strings.Contains(lower, "i/o error") || strings.Contains(lower, "io error") {
			detail := extractDiskDevice(line)
			events = append(events, model.SystemEvent{Ts: time.Now(), Kind: "disk_error", Detail: detail})
		}
	}
	return events
}

// linuxSshEvents counts failed SSH logins per source IP via journalctl.
func linuxSshEvents(ctx context.Context, since time.Time) []model.SystemEvent {
	var events []model.SystemEvent
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	sinceStr := since.Format("2006-01-02 15:04:05")
	out, err := runCmd(tctx, "journalctl", "-u", "sshd", "--since", sinceStr, "--no-pager", "-q")
	if err != nil {
		return events
	}
	// Count "Failed password" per source IP
	ipCount := map[string]int{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "Failed password") {
			continue
		}
		// "Failed password for root from 1.2.3.4 port 22 ssh2"
		ip := extractFromIP(line)
		if ip != "" {
			ipCount[ip]++
		}
	}
	const threshold = 5
	for ip, count := range ipCount {
		if count >= threshold {
			events = append(events, model.SystemEvent{
				Ts:     time.Now(),
				Kind:   "ssh_fail",
				Detail: ip + " (" + strconv.Itoa(count) + " attempts)",
			})
		}
	}
	return events
}

// linuxServiceRestartEvents detects systemd unit restarts via journalctl.
func linuxServiceRestartEvents(ctx context.Context, since time.Time) []model.SystemEvent {
	var events []model.SystemEvent
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	sinceStr := since.Format("2006-01-02 15:04:05")
	out, err := runCmd(tctx, "journalctl", "--since", sinceStr, "--no-pager", "-q",
		"--output=short-iso", "_SYSTEMD_UNIT=")
	if err != nil {
		return events
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		lower := strings.ToLower(line)
		if strings.Contains(lower, "started") && strings.Contains(lower, ".service") {
			unit := extractUnit(line)
			if unit != "" {
				events = append(events, model.SystemEvent{Ts: time.Now(), Kind: "service_restart", Detail: unit})
			}
		}
	}
	return events
}

func extractOOMProcess(line string) string {
	// "Out of memory: Kill process 1234 (myapp) score..."
	lower := strings.ToLower(line)
	idx := strings.Index(lower, "kill process")
	if idx < 0 {
		idx = strings.Index(lower, "killed process")
	}
	if idx < 0 {
		return ""
	}
	// Find the parenthesized name
	rest := line[idx:]
	s := strings.Index(rest, "(")
	e := strings.Index(rest, ")")
	if s >= 0 && e > s {
		return rest[s+1 : e]
	}
	return ""
}

func extractDiskDevice(line string) string {
	// Look for "dev sda", "/dev/sda", or device names like sda, nvme0n1
	for _, field := range strings.Fields(line) {
		field = strings.Trim(field, ",:()[]")
		if strings.HasPrefix(field, "/dev/") {
			return strings.TrimPrefix(field, "/dev/")
		}
		if len(field) >= 3 && isBlockDevName(field) {
			return field
		}
	}
	return ""
}

func isBlockDevName(s string) bool {
	return strings.HasPrefix(s, "sd") || strings.HasPrefix(s, "hd") ||
		strings.HasPrefix(s, "nvme") || strings.HasPrefix(s, "mmcblk")
}

func extractFromIP(line string) string {
	// "Failed password for root from 1.2.3.4 port 22"
	idx := strings.Index(line, " from ")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+len(" from "):])
	fields := strings.Fields(rest)
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func extractUnit(line string) string {
	// journalctl line contains "Started <unit.service>"
	lower := strings.ToLower(line)
	idx := strings.Index(lower, "started ")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len("started "):]
	fields := strings.Fields(rest)
	for _, f := range fields {
		f = strings.Trim(f, ".'\"")
		if strings.HasSuffix(f, ".service") {
			return f
		}
	}
	return ""
}

// ---- State structs for delta calculations ----

// netExtState holds inter-tick counters for extended net metrics.
type netExtState struct {
	snmpRetrans      uint64
	snmpRetransValid bool
	rxErrors         uint64
	txErrors         uint64
	netDevValid      bool
}

// sysLoadState holds inter-tick counters for SystemLoad.
type sysLoadState struct {
	ctxtTotal uint64
	ctxtTime  time.Time
	ctxtValid bool

	prevUptimeSeconds uint64
	uptimeValid       bool
}

// eventState holds the last-seen timestamp for event polling.
type eventState struct {
	lastPoll time.Time
}

// ---- Global state (guarded by linuxCollector.mu) ----

// We attach these to linuxCollector via embedding in an extension struct.
// Since linuxCollector is a private type, we store the extra state in separate
// package-level maps keyed by collector pointer — instead, we extend the
// linuxCollector struct directly below.
// (In Go, this is typically done by adding fields to the struct definition.
// Because the struct is defined in linux.go we add an init function below
// that populates them on first Collect. The fields are added via the extCollector wrapper.)

// linuxExtCollector wraps linuxCollector and adds v0.7 extended state.
type linuxExtCollector struct {
	*linuxCollector
	extMu      sync.Mutex
	netExt     netExtState
	sysLoad    sysLoadState
	evtState   eventState
	lastIface  string
}

// Ensure linuxExtCollector satisfies the Collector interface.
var _ Collector = (*linuxExtCollector)(nil)

// CollectEvents implements EventCollector for Linux.
func (c *linuxExtCollector) CollectEvents(ctx context.Context, since time.Time) []model.SystemEvent {
	c.extMu.Lock()
	defer c.extMu.Unlock()
	return linuxCollectEvents(ctx, since, &c.evtState)
}

// Collect delegates to the embedded linuxCollector then enriches the snapshot.
func (c *linuxExtCollector) Collect(ctx context.Context) (model.Snapshot, error) {
	snap, err := c.linuxCollector.Collect(ctx)
	if err != nil {
		return snap, err
	}

	c.extMu.Lock()
	iface := snap.Net.Interface
	if c.lastIface == "" {
		c.lastIface = iface
	}

	// Extended net (runs concurrently with the base Collect goroutines via the
	// 1s sleep in linuxNet; here we just read the already-collected base iface name).
	ext := linuxNetExt(ctx, iface, &c.netExt)
	c.extMu.Unlock()

	// Merge extended net into the base Net struct.
	snap.Net.OpenConnections = ext.OpenConnections
	snap.Net.PacketLossPct = ext.PacketLossPct
	snap.Net.LatencyMs = ext.LatencyMs
	snap.Net.TcpRetransmits = ext.TcpRetransmits
	snap.Net.RxErrors = ext.RxErrors
	snap.Net.TxErrors = ext.TxErrors

	// Extended disk: inodes, await, smart.
	mountToDev := linuxMountToDev()
	linuxInodesPct(ctx, snap.Disks)
	linuxAwaitMs(ctx, snap.Disks, mountToDev)
	linuxSmartStatus(ctx, snap.Disks, mountToDev)

	// SystemLoad.
	c.extMu.Lock()
	snap.SystemLoad = linuxSystemLoad(&c.sysLoad)
	c.extMu.Unlock()

	// WireGuard.
	snap.WireGuard = collectWireGuard(ctx)

	return snap, nil
}
