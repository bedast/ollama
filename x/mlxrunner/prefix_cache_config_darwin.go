//go:build darwin

package mlxrunner

import (
	"encoding/binary"
	"fmt"
	"syscall"
)

// hostPhysicalMemory reports total physical RAM on darwin. hw.memsize is
// exposed through sysctl(3) as a fixed-size 8-byte binary value;
// syscall.Sysctl returns that as a Go string whose bytes are the value in
// big-endian order.
func hostPhysicalMemory() (uint64, error) {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0, fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	const size = 8 // hw.memsize is a uint64
	if len(s) != size {
		return 0, fmt.Errorf("sysctl hw.memsize: got %d bytes, want %d", len(s), size)
	}
	return binary.BigEndian.Uint64([]byte(s)), nil
}
