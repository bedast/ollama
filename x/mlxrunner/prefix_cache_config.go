package mlxrunner

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/ollama/ollama/x/mlxrunner/mlx"
)

// The paged-out snapshot budget caps how much inactive prefix-cache snapshot
// memory the trie retains between requests (see enforceEvictionPolicy).
//
// The fixed 8 GiB default was sized for machines with large unified memory.
// On lower-RAM tiers an 18 GB model + 8 GiB of paged-out snapshots + OS
// overhead + live KV leaves no headroom, producing heavy swapping and OOM
// kills during long agentic sessions (see ollama issue #18131). The budget
// is therefore resolved once at startup:
//
//   - OLLAMA_MLX_PREFIX_CACHE_BYTES, when set, is used as-is: the point of
//     the override is operator control, including shrinking the budget below
//     the auto-sized floor on constrained machines.
//   - Otherwise the budget scales with total system RAM (total/32), clamped
//     to [2 GiB, 8 GiB]:
//
// RAM        total/32   clamped
// 16 GB       512 MiB   2 GiB
// 48 GB      1.5 GiB    2 GiB
// 64 GB      2 GiB      2 GiB
// 128 GB     4 GiB      4 GiB
// 256 GB+    8 GiB      8 GiB (the historical default)
//
// A 2 GiB floor keeps the cache useful for prompt-prefix reuse even on
// constrained machines; the ceiling preserves upstream behavior on
// high-memory hosts.

const (
	// minPagedOutBytes is the floor for the resolved snapshot budget. Below
	// this the prefix cache evicts too aggressively to retain useful prompt
	// prefixes between requests.
	minPagedOutBytes int64 = 2 << 30 // 2 GiB

	// legacyMaxPagedOutBytes is the historical fixed budget, retained as the
	// ceiling for the RAM-proportional default.
	legacyMaxPagedOutBytes int64 = 8 << 30 // 8 GiB

	// pagedOutRAMDivisor controls the RAM-proportional default: budget =
	// total RAM / divisor. total/32 reserves ~3% of physical RAM for
	// inactive prefix snapshots, leaving the rest for the model, the live
	// KV cache, and the OS.
	pagedOutRAMDivisor uint64 = 32
)

// envMaxPagedOutBytes returns the operator override from the environment, or
// 0 when unset/invalid. Invalid values log a warning and fall back to the
// RAM-scaled default rather than failing startup: a misconfigured cache
// budget should degrade to the default, not kill the runner.
func envMaxPagedOutBytes() int64 {
	s := strings.TrimSpace(os.Getenv("OLLAMA_MLX_PREFIX_CACHE_BYTES"))
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		slog.Warn("invalid OLLAMA_MLX_PREFIX_CACHE_BYTES, using RAM-scaled default",
			"value", s)
		return 0
	}
	return n
}

// resolvePagedOutBudget computes the effective snapshot budget from an
// operator override and total system RAM. It is a pure function of its
// inputs so tests can exercise every tier without touching the process
// environment.
//
// A positive envValue wins as-is. Otherwise the default scales with
// totalRAM. totalRAM == 0 (unknown) falls back to the legacy 8 GiB default:
// without knowledge of the machine, preserve historical behavior.
func resolvePagedOutBudget(envValue int64, totalRAM uint64) int64 {
	if envValue > 0 {
		// An explicit operator override is honored as-is: the point of
		// OLLAMA_MLX_PREFIX_CACHE_BYTES is control, including lowering the
		// budget below the auto-sized floor on constrained machines.
		return envValue
	}
	if totalRAM == 0 {
		return legacyMaxPagedOutBytes
	}
	scaled := totalRAM / pagedOutRAMDivisor
	if scaled > uint64(legacyMaxPagedOutBytes) {
		return legacyMaxPagedOutBytes
	}
	if scaled < uint64(minPagedOutBytes) {
		return minPagedOutBytes
	}
	return int64(scaled)
}

// pagedOutBudget resolves the effective snapshot budget once per process.
// The first resolution logs the outcome so operators can see which tier of
// budget the runner chose and why.
var pagedOutBudget = sync.OnceValue(func() int64 {
	envVal := envMaxPagedOutBytes()
	totalRAM, err := hostPhysicalMemory()
	if err != nil {
		// Unknown RAM: fall back to the legacy fixed budget. Do not fail
		// startup over a missing memory probe.
		slog.Debug("unable to determine total system memory for prefix cache budget", "error", err)
		return resolvePagedOutBudget(envVal, 0)
	}
	budget := resolvePagedOutBudget(envVal, totalRAM)
	if envVal > 0 {
		slog.Info("prefix cache budget set by OLLAMA_MLX_PREFIX_CACHE_BYTES",
			"budget", mlx.PrettyBytes(int(budget)))
	} else {
		slog.Info("prefix cache budget scaled to system memory",
			"total", mlx.PrettyBytes(int(totalRAM)),
			"budget", mlx.PrettyBytes(int(budget)))
	}
	return budget
})
