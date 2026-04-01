//go:build linux || darwin

// internal/collector/gpu_nvidia.go
package collector

import (
	"context"
	"strconv"
	"strings"
	"time"

	"sumi/internal/model"
)

// collectNvidiaGPU tries to read GPU metrics via nvidia-smi.
// Returns nil if nvidia-smi is not found or fails.
func collectNvidiaGPU(ctx context.Context) *model.GPUInfo {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Ask for: name, utilization.gpu, temperature.gpu, memory.used, memory.total
	out, err := runCmd(tctx, "nvidia-smi",
		"--query-gpu=name,utilization.gpu,temperature.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits")
	if err != nil {
		return nil
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return nil
	}
	parts := strings.SplitN(line, ",", 5)
	if len(parts) < 5 {
		return nil
	}
	name := strings.TrimSpace(parts[0])
	usage, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return nil
	}
	temp, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil {
		return nil
	}
	vramUsed, err := strconv.ParseUint(strings.TrimSpace(parts[3]), 10, 64)
	if err != nil {
		return nil
	}
	vramTotal, err := strconv.ParseUint(strings.TrimSpace(parts[4]), 10, 64)
	if err != nil {
		return nil
	}
	return &model.GPUInfo{
		Name:         name,
		Driver:       "nvidia",
		UsagePct:     usage,
		TempC:        temp,
		VRAMUsedMiB:  vramUsed,
		VRAMTotalMiB: vramTotal,
	}
}
