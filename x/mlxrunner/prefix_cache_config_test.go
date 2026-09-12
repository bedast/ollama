package mlxrunner

import (
	"runtime"
	"slices"
	"testing"

	"github.com/ollama/ollama/x/mlxrunner/cache"
)

// TestResolvePagedOutBudget verifies the budget resolution rules: operator
// override wins (clamped to the floor), RAM-proportional default with
// clamping, and the legacy fallback when total RAM is unknown.
func TestResolvePagedOutBudget(t *testing.T) {
	gib := int64(1) << 30
	tests := []struct {
		name     string
		envValue int64
		totalRAM uint64
		want     int64
	}{
		// Operator override wins.
		{"env positive", 1 * gib, 64 << 30, 1 * gib},
		{"env large", 16 * gib, 256 << 30, 16 * gib},
		// Overrides below the auto-sized floor are honored as-is (operator control).
		{"env below floor", 512 << 20, 64 << 30, 512 << 20},
		{"env zero falls through", 0, 48 << 30, minPagedOutBytes},
		{"env negative falls through", -4, 48 << 30, minPagedOutBytes},

		// RAM-proportional defaults.
		{"16GB", 0, 16 << 30, minPagedOutBytes},         // 512 MiB -> floor
		{"48GB", 0, 48 << 30, minPagedOutBytes},         // 1.5 GiB -> floor
		{"64GB", 0, 64 << 30, minPagedOutBytes},         // 2 GiB
		{"128GB", 0, 128 << 30, 4 * gib},                // 4 GiB
		{"192GB", 0, 192 << 30, 6 * gib},                // 6 GiB
		{"256GB", 0, 256 << 30, legacyMaxPagedOutBytes}, // 8 GiB ceiling
		{"512GB", 0, 512 << 30, legacyMaxPagedOutBytes}, // 16 GiB -> ceiling

		// Unknown RAM keeps the legacy budget.
		{"unknown RAM", 0, 0, legacyMaxPagedOutBytes},
		{"unknown RAM with env", 3 * gib, 0, 3 * gib},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePagedOutBudget(tt.envValue, tt.totalRAM); got != tt.want {
				t.Fatalf("resolvePagedOutBudget(%d, %d) = %d, want %d",
					tt.envValue, tt.totalRAM, got, tt.want)
			}
		})
	}
}

// TestHostPhysicalMemory verifies the RAM probe returns a plausible value on
// supported platforms. It is a smoke test: the exact value is machine-dependent.
func TestHostPhysicalMemory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("no RAM probe on %s", runtime.GOOS)
	}
	total, err := hostPhysicalMemory()
	if err != nil {
		t.Fatalf("hostPhysicalMemory: %v", err)
	}
	const floor = uint64(1) << 30 // any machine this code runs on has >= 1 GiB
	if total < floor {
		t.Fatalf("hostPhysicalMemory = %d, want >= %d", total, floor)
	}
}

// TestPagedOutBudgetLimitZeroValue verifies that a prefixCache constructed
// without a limit (tests, legacy call sites) keeps the legacy fixed budget.
func TestPagedOutBudgetLimitZeroValue(t *testing.T) {
	pc := &prefixCache{}
	if got, want := pc.pagedOutBudgetLimit(), maxPagedOutBytes; got != want {
		t.Fatalf("zero-value limit = %d, want legacy %d", got, want)
	}
	pc.pagedOutLimit = 3 << 30
	if got, want := pc.pagedOutBudgetLimit(), int64(3<<30); got != want {
		t.Fatalf("explicit limit = %d, want %d", got, want)
	}
}

// TestEnforceEvictionPolicyExplicitLimit verifies that eviction honors an
// explicit non-default pagedOutLimit rather than the package constant.
func TestEnforceEvictionPolicyExplicitLimit(t *testing.T) {
	env := newTransformerEnv()
	pc := env.pc
	pc.pagedOutLimit = 1 << 30 // 1 GiB — far below the legacy 8 GiB

	// Five conversations sharing a system prompt, each with a unique
	// prompt suffix plus its own generated tail — the same shape as
	// TestEvictionPreservesActiveConversations. The branch point is
	// protected; the five leaf branches are evictable.
	systemPrompt := []int32{1, 2, 3, 4, 5}
	for i := range 5 {
		suffix := []int32{int32(100 + i*10), int32(101 + i*10), int32(102 + i*10)}
		inputs := append(slices.Clone(systemPrompt), suffix...)
		simulateRequest(t, pc, inputs, []int32{int32(200 + i)})
	}

	// Inflate snapshots past the explicit limit.
	walkNodes(pc.root, func(n *trieNode) bool {
		if !n.hasSnapshots() {
			return true
		}
		snaps := make([]cache.Snapshot, len(n.snapshots))
		for i, s := range n.snapshots {
			if s != nil {
				snaps[i] = &fakeSnapshot{byteSize: 512 << 20}
			}
		}
		n.setSnapshots(snaps, &pc.pagedOutBytes)
		return true
	})
	if pc.pagedOutBytes <= pc.pagedOutLimit {
		t.Fatalf("setup failed: pagedOutBytes = %d, want > %d", pc.pagedOutBytes, pc.pagedOutLimit)
	}

	pc.enforceEvictionPolicy()

	if pc.pagedOutBytes > pc.pagedOutLimit {
		t.Fatalf("pagedOutBytes = %d, want <= %d", pc.pagedOutBytes, pc.pagedOutLimit)
	}
	if len(pc.activePath) < 1 {
		t.Fatal("activePath should retain at least the root")
	}
	checkTrieInvariants(t, pc.root)
}

// TestNewPrefixCacheUsesResolvedBudget verifies that the production
// constructor installs the resolved budget rather than the legacy constant.
func TestNewPrefixCacheUsesResolvedBudget(t *testing.T) {
	pc := newPrefixCache(nil)
	if pc.pagedOutLimit == 0 {
		t.Fatal("newPrefixCache left pagedOutLimit unset")
	}
	// The resolved value must be one of the legal outputs of the resolver.
	ram, err := hostPhysicalMemory()
	if err != nil {
		ram = 0
	}
	want := resolvePagedOutBudget(envMaxPagedOutBytes(), ram)
	if pc.pagedOutLimit != want {
		t.Fatalf("pagedOutLimit = %d, want %d (resolved for this machine)", pc.pagedOutLimit, want)
	}
}
