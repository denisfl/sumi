//go:build linux

// internal/collector/rpi.go
package collector

import (
	"context"
	"os"
	"strconv"
	"strings"

	"sumi/internal/model"
)

// isRaspberryPi checks /proc/device-tree/model for "Raspberry Pi".
func isRaspberryPi() bool {
	data, err := os.ReadFile("/proc/device-tree/model")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "Raspberry Pi")
}

type rpiCollector struct {
	base *linuxCollector
}

func (c *rpiCollector) Collect(ctx context.Context) (model.Snapshot, error) {
	s, err := c.base.Collect(ctx)
	if err != nil {
		return s, err
	}
	s.Platform = "rpi"
	s.Thermal = rpiThermal(ctx)
	return s, nil
}

// rpiThermal reads temperature, frequencies and throttle state via vcgencmd.
func rpiThermal(ctx context.Context) model.Thermal {
	t := model.Thermal{}

	// Temperature: "temp=42.8'C"
	if out, err := runCmd(ctx, "vcgencmd", "measure_temp"); err == nil {
		out = strings.TrimSpace(out)
		out = strings.TrimPrefix(out, "temp=")
		out = strings.TrimSuffix(out, "'C")
		if v, err := strconv.ParseFloat(out, 64); err == nil {
			t.TempC = v
		}
	}

	// ARM frequency: "frequency(48)=1800000000"
	if out, err := runCmd(ctx, "vcgencmd", "measure_clock", "arm"); err == nil {
		t.ArmFreqMHz = parseVcgFreq(out)
	}

	// Core/GPU frequency
	if out, err := runCmd(ctx, "vcgencmd", "measure_clock", "core"); err == nil {
		t.GpuFreqMHz = parseVcgFreq(out)
	}

	// Throttle state: "throttled=0x0"
	if out, err := runCmd(ctx, "vcgencmd", "get_throttled"); err == nil {
		out = strings.TrimSpace(out)
		out = strings.TrimPrefix(out, "throttled=")
		if out != "0x0" && out != "0x00000000" {
			t.Throttled = out
		}
	}

	// Populate Sensors from the readings above.
	if t.TempC > 0 {
		t.Sensors = append(t.Sensors, model.ThermalSensor{Name: "CPU", TempC: t.TempC})
	}

	return t
}

// parseVcgFreq extracts frequency in MHz from "frequency(48)=1800000000".
func parseVcgFreq(out string) int {
	out = strings.TrimSpace(out)
	parts := strings.SplitN(out, "=", 2)
	if len(parts) != 2 {
		return 0
	}
	hz, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return 0
	}
	return int(hz / 1_000_000)
}
