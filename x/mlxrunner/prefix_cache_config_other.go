//go:build !darwin && !linux

package mlxrunner

import "fmt"

// hostPhysicalMemory reports total physical RAM on platforms without a
// portable probe. It always errors so the resolver falls back to the legacy
// fixed budget rather than guessing.
func hostPhysicalMemory() (uint64, error) {
	return 0, fmt.Errorf("total RAM discovery not supported on this platform")
}
