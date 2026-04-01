// internal/collector/procdetail_darwin.go
package collector

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"sumi/internal/model"
)

// ReadProcDetail reads extended info for a process using ps on macOS.
// Returns nil when the process no longer exists.
func ReadProcDetail(pid int) *model.ProcDetail {
	ctx := context.Background()
	// ps -p <pid> -o pid,ppid,nlwp,lstart,comm
	out, err := runCmd(ctx, "ps", "-p", strconv.Itoa(pid),
		"-o", "pid=,ppid=,nlwp=,lstart=,comm=")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 4 {
		return nil
	}
	d := &model.ProcDetail{PID: pid}
	d.PPID, _ = strconv.Atoi(fields[1])
	d.Threads, _ = strconv.Atoi(fields[2])
	// lstart is typically 4 fields (Day Mon DD HH:MM:SS YYYY), comm is last
	if len(fields) >= 9 {
		d.StartTime = strings.Join(fields[3:8], " ")
		d.Name = fields[len(fields)-1]
	}

	// FD count via lsof -p <pid> -F (count lines).
	if lsofOut, err := runCmd(ctx, "lsof", "-p", strconv.Itoa(pid), "-Fn"); err == nil {
		d.FDs = strings.Count(lsofOut, "\n")
	}

	// cwd from lsof -p <pid> -a -d cwd -Fn.
	if cwdOut, err := runCmd(ctx, "lsof", "-p", strconv.Itoa(pid), "-a", "-d", "cwd", "-Fn"); err == nil {
		for _, line := range strings.Split(cwdOut, "\n") {
			if strings.HasPrefix(line, "n") {
				d.Cwd = strings.TrimPrefix(line, "n")
				break
			}
		}
	}

	if d.Name == "" {
		d.Name = fmt.Sprintf("pid:%d", pid)
	}
	return d
}
