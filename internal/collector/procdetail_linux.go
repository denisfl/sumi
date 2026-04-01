// internal/collector/procdetail_linux.go
package collector

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"sumi/internal/model"
)

// ReadProcDetail reads extended info for a process from /proc/PID.
// Returns nil when the process no longer exists.
func ReadProcDetail(pid int) *model.ProcDetail {
	base := fmt.Sprintf("/proc/%d", pid)

	// /proc/PID/status: Name, PPid, Threads
	d := &model.ProcDetail{PID: pid}
	if statusData, err := os.ReadFile(base + "/status"); err == nil {
		for _, line := range strings.Split(string(statusData), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			switch key {
			case "Name":
				d.Name = val
			case "PPid":
				d.PPID, _ = strconv.Atoi(val)
			case "Threads":
				d.Threads, _ = strconv.Atoi(val)
			}
		}
	} else {
		return nil // process is gone
	}

	// FD count via /proc/PID/fd directory listing.
	if fds, err := os.ReadDir(base + "/fd"); err == nil {
		d.FDs = len(fds)
	}

	// Current working directory via /proc/PID/cwd symlink.
	if cwd, err := os.Readlink(base + "/cwd"); err == nil {
		d.Cwd = cwd
	}

	// Start time from /proc/PID/stat field 22 (starttime in jiffies since boot).
	// Use /proc/uptime + stat to compute a human-readable start time.
	if statData, err := os.ReadFile(base + "/stat"); err == nil {
		d.StartTime = parseProcStartTime(string(statData))
	}

	return d
}

// parseProcStartTime extracts a human-readable start time from /proc/PID/stat content.
func parseProcStartTime(stat string) string {
	// Format: pid (name) state ppid pgrp ... starttime(22nd field) ...
	// The name can contain spaces and is wrapped in parentheses.
	closeParen := strings.LastIndex(stat, ")")
	if closeParen < 0 {
		return ""
	}
	rest := strings.TrimSpace(stat[closeParen+1:])
	fields := strings.Fields(rest)
	// Field 22 overall = field 19 after "state ppid pgrp session ttyNr tpgid flags..."
	// After the closing paren: state(0) ppid(1) pgrp(2) session(3) ttyNr(4) tpgid(5)
	//   flags(6) minflt(7) cminflt(8) majflt(9) cmajflt(10) utime(11) stime(12)
	//   cutime(13) cstime(14) priority(15) nice(16) num_threads(17) itrealvalue(18) starttime(19)
	if len(fields) < 20 {
		return ""
	}
	startJiffies, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ""
	}
	// Read boot time from /proc/stat: "btime <unix_timestamp>"
	bootTime := uint64(0)
	if bdata, err := os.ReadFile("/proc/stat"); err == nil {
		for _, line := range strings.Split(string(bdata), "\n") {
			if strings.HasPrefix(line, "btime ") {
				bt, err := strconv.ParseUint(strings.TrimSpace(line[6:]), 10, 64)
				if err == nil {
					bootTime = bt
				}
				break
			}
		}
	}
	if bootTime == 0 {
		return fmt.Sprintf("tick %d", startJiffies)
	}
	// Assume 100 jiffies/second (USER_HZ=100, typical on Linux).
	startSec := bootTime + startJiffies/100
	return fmt.Sprintf("%ds uptime-offset", startSec)
}
