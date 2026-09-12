//go:build linux

package mlxrunner

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// hostPhysicalMemory reports total physical RAM on linux via /proc/meminfo.
func hostPhysicalMemory() (uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		after, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(after)
		if len(fields) == 0 {
			return 0, fmt.Errorf("parse /proc/meminfo MemTotal: %q", line)
		}
		kib, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse /proc/meminfo MemTotal value %q: %w", fields[0], err)
		}
		return kib * 1024, nil // MemTotal is reported in KiB
	}
	return 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
}
