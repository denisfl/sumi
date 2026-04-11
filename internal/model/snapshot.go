// internal/model/snapshot.go
package model

import "time"

// ProcEntry holds a single process entry for the top-processes list.
type ProcEntry struct {
	Name      string
	PID       int
	CPUPct    float64
	MemPct    float64
	Container string // "docker", "k8s", or "" for bare-metal processes
}

// ThermalSensor holds a named temperature reading.
type ThermalSensor struct {
	Name  string  `json:"Name"`
	TempC float64 `json:"TempC"`
}

// Thermal holds temperature and frequency data, populated on Raspberry Pi
// or from /sys/class/thermal on Linux.
type Thermal struct {
	TempC      float64          // primary CPU temperature (kept for backward compat)
	Sensors    []ThermalSensor  // named sensor list (CPU, GPU, SSD, etc.)
	ArmFreqMHz int
	GpuFreqMHz int
	// Throttled is the raw hex string from vcgencmd get_throttled.
	// Empty string means no throttling (0x0).
	Throttled string
}

// CPU holds CPU metrics.
type CPU struct {
	Usage      float64
	Cores      int
	Model      string
	TempC      float64
	CoreUsages []float64 // per-core usage 0–100; nil if unavailable on this platform
}

// Mem holds memory metrics in bytes.
type Mem struct {
	UsedBytes  uint64
	TotalBytes uint64
	FreeBytes  uint64
	SwapUsed   uint64
	SwapTotal  uint64
}

// DiskInfo holds disk usage metrics in bytes for a single mount point.
type DiskInfo struct {
	UsedBytes     uint64
	TotalBytes    uint64
	FreeBytes     uint64
	MountPoint    string
	FSType        string
	ReadKBps      float64 // read throughput in KB/s since last sample (0 when unavailable)
	WriteKBps     float64 // write throughput in KB/s since last sample (0 when unavailable)
	InodesUsedPct float64 // inode saturation 0–100; 0 if filesystem has no inodes
	AwaitMs       float64 // avg I/O service time in ms (Linux only, 0 on macOS)
	SmartStatus   string  // "ok", "warn", "fail", or "" if smartctl unavailable
}

// GPUInfo holds GPU metrics. Nil when no supported GPU tool is available.
type GPUInfo struct {
	Name         string
	Driver       string  // "nvidia", "amd", "apple"
	UsagePct     float64
	TempC        float64
	VRAMUsedMiB  uint64
	VRAMTotalMiB uint64
}

// Net holds network interface metrics.
type Net struct {
	Interface      string
	IP             string
	RxKBps         float64
	TxKBps         float64
	OpenConnections int     // total established TCP connections; 0 if unavailable
	PacketLossPct   float64 // gateway ping packet loss 0–100; -1 if unavailable
	LatencyMs       float64 // gateway ping avg RTT in ms; -1 if unavailable
	TcpRetransmits  uint64  // delta TCP retransmit segments since last tick (Linux only)
	RxErrors        uint64  // cumulative RX error delta since last tick
	TxErrors        uint64  // cumulative TX error delta since last tick
}

// SystemLoad holds OS-level system pressure metrics outside of CPU/Mem.
type SystemLoad struct {
	Load1                 float64 // 1-minute load average
	Load5                 float64 // 5-minute load average
	Load15                float64 // 15-minute load average
	UptimeSeconds         uint64  // seconds since last boot
	FdUsedPct             float64 // open file-descriptor saturation 0–100
	ZombieProcs           int     // number of zombie processes
	ContextSwitchesPerSec float64 // context switches per second since last tick
}

// SystemEvent represents a discrete OS-level event detected by the event collector.
type SystemEvent struct {
	Ts     time.Time `json:"ts"`
	Kind   string    `json:"kind"`   // "oom_kill", "reboot", "ssh_fail", "disk_error", "service_restart"
	Detail string    `json:"detail"` // human-readable detail, may be empty
}

// WireGuardInfo holds aggregate metrics for all WireGuard interfaces on the host.
// Nil when the wg tool is not installed or no WireGuard interfaces exist.
type WireGuardInfo struct {
	Interface         string // first interface name (or comma-joined if multiple)
	PeersTotal        int    // total configured peer count
	PeersOnline       int    // peers with last-handshake within 180 s
	LastHandshakeAge  int64  // seconds since most recent handshake across all peers
	TransferRxBytes   int64  // cumulative RX bytes for the interface
	TransferTxBytes   int64  // cumulative TX bytes for the interface
}

// History holds pre-computed sparkline strings for the TUI renderer.
// Empty when running in single-shot mode.
type History struct {
	CPUSpark   string
	MemSpark   string
	NetRxSpark string // Rx throughput history (green)
	NetTxSpark string // Tx throughput history (orange)
}

// BatteryInfo holds battery status. Nil when no battery is detected.
type BatteryInfo struct {
	ChargePct float64 // current charge 0–100
	Charging  bool    // true when plugged in and charging
	TimeLeft  string  // human-readable time remaining ("2h 15m", "Charged", etc.)
}

// ProcDetail holds extended per-process info shown in the detail panel.
type ProcDetail struct {
	PID       int
	Name      string
	PPID      int
	Threads   int
	FDs       int    // open file-descriptor count
	Cwd       string // current working directory
	StartTime string // human-readable process start time
	CPUSpark  string // sparkline of recent CPU usage (from history ring)
	MemSpark  string // sparkline of recent Mem usage (from history ring)
}

// DBConnections holds the connection pool state for a database instance.
type DBConnections struct {
	Active  int
	Idle    int
	Waiting int
	Max     int
}

// NormalizedQuery holds aggregated stats for one normalised query pattern.
type NormalizedQuery struct {
	QueryHash string  // hex SHA-256 of the normalised template
	Calls     int64
	TotalMs   float64
	MeanMs    float64
	Template  string  // query text with literals replaced by placeholders
}

// DBSnapshot holds one point-in-time snapshot for a single configured database.
type DBSnapshot struct {
	Name            string
	Driver          string           // "postgres" | "mysql"
	Connections     DBConnections
	QueryThroughput float64          // queries/s since last tick; 0 when unavailable
	AvgLatencyMs    float64          // mean query latency ms; 0 when unavailable
	P95LatencyMs    float64          // p95 query latency ms; 0 when unavailable
	ActiveLockCount int              // number of ungranted / blocked lock requests
	SlowQueries     []NormalizedQuery // top-5 by total_time since last tick
	ReplicationLagS float64          // seconds of replica lag; -1 if not a replica or unavailable
	Error           string           // non-empty when collector encountered an error this tick
}

// Snapshot is the complete point-in-time view of system metrics.
// Any field that could not be collected is left at its zero value.
type Snapshot struct {
	CPU       CPU
	Mem       Mem
	Disks     []DiskInfo   // all mounted filesystems (virtual FSes excluded)
	Net       Net
	Procs     []ProcEntry
	Thermal   Thermal
	GPU       *GPUInfo     // nil when no GPU tool is available
	Battery   *BatteryInfo // nil when no battery is present
	History   History      // sparkline strings; empty in single-shot mode
	Platform  string       // "linux", "darwin", or "rpi"
	Hostname  string
	Uptime    string // human-readable uptime string
	Timestamp time.Time

	SystemLoad  SystemLoad     // OS-level pressure metrics
	WireGuard   *WireGuardInfo // nil when wg not present

	// Injected by the push goroutine; absent from normal TUI / NDJSON output.
	DeviceID      string `json:",omitempty"`
	ClientVersion string `json:",omitempty"`

	// UpdateAvailable is the latest release tag when a newer version has been
	// detected by the background update checker. Empty when up to date.
	UpdateAvailable string `json:",omitempty"`

	// Databases holds snapshots for each configured [[database]] entry.
	// Empty when no databases are configured.
	Databases []DBSnapshot `json:",omitempty"`
}
