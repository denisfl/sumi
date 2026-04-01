// internal/model/snapshot.go
package model

import "time"

// ProcEntry holds a single process entry for the top-processes list.
type ProcEntry struct {
	Name   string
	PID    int
	CPUPct float64
	MemPct float64
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
	UsedBytes  uint64
	TotalBytes uint64
	FreeBytes  uint64
	MountPoint string
	FSType     string
	ReadKBps   float64 // read throughput in KB/s since last sample (0 when unavailable)
	WriteKBps  float64 // write throughput in KB/s since last sample (0 when unavailable)
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
	Interface string
	IP        string
	RxKBps    float64
	TxKBps    float64
}

// History holds pre-computed sparkline strings for the TUI renderer.
// Empty when running in single-shot mode.
type History struct {
	CPUSpark   string
	MemSpark   string
	NetRxSpark string // Rx throughput history (green)
	NetTxSpark string // Tx throughput history (orange)
}

// Snapshot is the complete point-in-time view of system metrics.
// Any field that could not be collected is left at its zero value.
type Snapshot struct {
	CPU       CPU
	Mem       Mem
	Disks     []DiskInfo // all mounted filesystems (virtual FSes excluded)
	Net       Net
	Procs     []ProcEntry
	Thermal   Thermal
	GPU       *GPUInfo   // nil when no GPU tool is available
	History   History    // sparkline strings; empty in single-shot mode
	Platform  string // "linux", "darwin", or "rpi"
	Hostname  string
	Uptime    string // human-readable uptime string
	Timestamp time.Time
}
