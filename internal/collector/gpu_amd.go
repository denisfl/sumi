//go:build linux

// internal/collector/gpu_amd.go
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sumi/internal/model"
)

// collectAMDGPU tries to read GPU metrics via rocm-smi.
// Returns nil if rocm-smi is not found or fails.
func collectAMDGPU(ctx context.Context) *model.GPUInfo {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := runCmd(tctx, "rocm-smi", "--showproductname", "--showuse",
		"--showtemp", "--showmemuse", "--json")
	if err != nil {
		return nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	// rocm-smi --json outputs a map of card0 → {fields}
	var raw map[string]map[string]string
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil
	}
	// Use first GPU found.
	for _, fields := range raw {
		gpu := &model.GPUInfo{Driver: "amd"}
		if v, ok := fields["Card series"]; ok {
			gpu.Name = strings.TrimSpace(v)
		}
		if v, ok := fields["GPU use (%)"]; ok {
			_, _ = fmt.Sscanf(v, "%f", &gpu.UsagePct)
		}
		if v, ok := fields["Temperature (Sensor junction) (C)"]; ok {
			_, _ = fmt.Sscanf(v, "%f", &gpu.TempC)
		}
		if v, ok := fields["GPU memory use (%)"]; ok {
			var pct float64
			_, _ = fmt.Sscanf(v, "%f", &pct)
			_ = pct // approximate; rocm-smi doesn't always expose absolute MiB
		}
		if gpu.Name == "" && gpu.UsagePct == 0 {
			continue
		}
		return gpu
	}
	return nil
}
