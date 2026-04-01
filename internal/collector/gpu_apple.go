//go:build darwin

// internal/collector/gpu_apple.go
// Apple Silicon GPU metrics via powermetrics (requires sudo -n).
// Gracefully returns nil when access is not available.
package collector

import (
	"bufio"
	"context"
	"strconv"
	"strings"
	"time"

	"sumi/internal/model"
)

// collectAppleGPU tries powermetrics with sudo -n (no-password).
// Returns nil gracefully when powermetrics is unavailable or sudo requires a password.
func collectAppleGPU(ctx context.Context) *model.GPUInfo {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// -n 1 = one sample, -i 0 = minimal interval, --samplers gpu_power
	out, err := runCmd(tctx, "sudo", "-n", "powermetrics",
		"-n", "1", "-i", "100", "--samplers", "gpu_power", "-f", "plist")
	if err != nil {
		return nil
	}
	return parseAppleGPUPlist(out)
}

// parseAppleGPUPlist extracts GPU active residency and frequency from powermetrics plist.
// The plist will have a gpu_power section; we extract what we can without a full XML parser.
func parseAppleGPUPlist(out string) *model.GPUInfo {
	gpu := &model.GPUInfo{
		Name:   "Apple GPU",
		Driver: "apple",
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	var lastKey string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "<key>") {
			lastKey = strings.TrimSuffix(strings.TrimPrefix(line, "<key>"), "</key>")
		} else if strings.HasPrefix(line, "<real>") || strings.HasPrefix(line, "<integer>") {
			valStr := line
			valStr = strings.TrimPrefix(valStr, "<real>")
			valStr = strings.TrimPrefix(valStr, "<integer>")
			valStr = strings.TrimSuffix(valStr, "</real>")
			valStr = strings.TrimSuffix(valStr, "</integer>")
			v, err := strconv.ParseFloat(strings.TrimSpace(valStr), 64)
			if err != nil {
				continue
			}
			switch lastKey {
			case "gpu_active_residency":
				gpu.UsagePct = v * 100.0
			}
		}
	}
	// Only return GPU if we got at least usage data.
	if gpu.UsagePct == 0 {
		return nil
	}
	return gpu
}
