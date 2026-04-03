// internal/collector/ext_parsers.go — Pure parser functions for v0.7 extended
// metrics. No build tag: these are shared between Linux and macOS implementations
// and can be unit-tested on any platform.
package collector

import (
	"bufio"
	"strconv"
	"strings"
)

// parsePingStats extracts (packetLossPct, avgMs) from ping output.
// Handles Linux and macOS ping output format.
// Returns -1, -1 if the relevant lines are not found.
func parsePingStats(out string) (loss, avgMs float64) {
	loss = -1
	avgMs = -1
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		// Packet loss line: "3 packets transmitted, 3 received, 0% packet loss"
		if strings.Contains(line, "packet loss") {
			for _, field := range strings.Fields(line) {
				if strings.HasSuffix(field, "%") {
					if v, err := strconv.ParseFloat(strings.TrimSuffix(field, "%"), 64); err == nil {
						loss = v
					}
				}
			}
		}
		// Linux RTT: "rtt min/avg/max/mdev = 1.2/2.3/3.4/0.5 ms"
		// macOS RTT: "round-trip min/avg/max/stddev = 1.2/2.3/3.4/0.5 ms"
		if strings.HasPrefix(line, "rtt ") || strings.HasPrefix(line, "round-trip") {
			parts := strings.Split(line, "=")
			if len(parts) < 2 {
				continue
			}
			vals := strings.Split(strings.TrimSpace(parts[1]), "/")
			if len(vals) >= 2 {
				if v, err := strconv.ParseFloat(vals[1], 64); err == nil {
					avgMs = v
				}
			}
		}
	}
	return loss, avgMs
}

// parseSmartHealth parses `smartctl -H` output and returns "ok", "warn", "fail", or "".
func parseSmartHealth(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "self-assessment test result:") || strings.Contains(line, "overall-health") {
			upper := strings.ToUpper(line)
			if strings.Contains(upper, "PASSED") || strings.Contains(upper, "OK") {
				return "ok"
			}
			if strings.Contains(upper, "FAILED") {
				return "fail"
			}
			if strings.Contains(upper, "PRE-FAILURE") || strings.Contains(upper, "ADVISORY") {
				return "warn"
			}
		}
	}
	return ""
}

// parseDfInodes parses `df -i` output into a map of mountpoint → InodesUsedPct.
// Works for both Linux (`df -i`) and macOS (`df -i`) output formats.
func parseDfInodes(out string) map[string]float64 {
	m := map[string]float64{}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		mount := fields[len(fields)-1]
		// Linux: Filesystem Inodes IUsed IFree IUse% Mounted
		// macOS:  Filesystem 512-Blks Used Available Cap% iused ifree %iused Mounted
		// Try Linux format (fields[2]=IUsed, fields[1]=ITotal) first.
		iTotal, err1 := strconv.ParseUint(fields[1], 10, 64)
		iUsed, err2 := strconv.ParseUint(fields[2], 10, 64)
		if err1 == nil && err2 == nil && iTotal > 0 {
			m[mount] = float64(iUsed) / float64(iTotal) * 100.0
			continue
		}
		// macOS fallback: iused at len-4, ifree at len-3
		if len(fields) >= 9 {
			iused, e1 := strconv.ParseUint(fields[len(fields)-4], 10, 64)
			ifree, e2 := strconv.ParseUint(fields[len(fields)-3], 10, 64)
			if e1 == nil && e2 == nil {
				iTotal = iused + ifree
				if iTotal > 0 {
					m[mount] = float64(iused) / float64(iTotal) * 100.0
				}
			}
		}
	}
	return m
}

// parseSsEstab parses the "estab" count from `ss -s` output.
// Example line: "TCP:   12 (estab 5, closed 3, orphaned 0, timewait 4)"
func parseSsEstab(out string) int {
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "TCP:") {
			continue
		}
		lower := strings.ToLower(line)
		idx := strings.Index(lower, "estab ")
		if idx < 0 {
			idx = strings.Index(lower, "estab")
		}
		if idx < 0 {
			continue
		}
		rest := line[idx+len("estab "):]
		rest = strings.TrimSpace(rest)
		parts := strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == ')' || r == ' ' })
		if len(parts) > 0 {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				return n
			}
		}
	}
	return 0
}
