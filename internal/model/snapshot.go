// internal/model/snapshot.go
package model

import "time"

// ProcEntry holds a single process entry for the top-processes list.
type ProcEntry struct {
	Name   string
	CPUPct float64
	MemPct float64
}

// Thermal holds temperature and frequency data, populated on Raspberry Pi
// or from /sys/class/thermal on Linux.
type Thermal struct {
	TempC      float64
	ArmFreqMHz int
	GpuFreqMHz int
	// Throttled is the raw hex string from vcgencmd get_throttled.
	// Empty string means no throttling (0x0).
	Throttled string
}

// CPU holds CPU metrics.
type CPU struct {
	Usage  float64
	Cores  int
	Model  string
	TempC  float64
}

// Mem holds memory metrics in bytes.
type Mem struct {
	UsedBytes  uint64
	TotalBytes uint64
	FreeBytes  uint64
	SwapUsed   uint64
	SwapTotal  uint64
}

// Disk holds disk usage metrics in bytes.
type Disk struct {
	UsedBytes  uint64
	TotalBytes uint64
	FreeBytes  uint64
	MountPoint string
}

// Net holds network interface metrics.
type Net struct {
	Interface string
	IP        string
	RxKBps    float64
	TxKBps    float64
}

// Snapshot is the complete point-in-time view of system metrics.
// Any field that could not be collected is left at its zero value.
type Snapshot struct {
	CPU       CPU
	Mem       Mem
	Disk      Disk
	Net       Net
	Procs     []ProcEntry
	Thermal   Thermal
	Platform  string // "linux", "darwin", or "rpi"
	Hostname  string
	Uptime    string // human-readable uptime string
	Timestamp time.Time
}
