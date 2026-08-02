package harness

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	registryMu sync.Mutex
	registry   = map[string]Profile{}
)

// Register adds profile to the matrix, keyed by its lower-cased Name. Server
// packages call this from an init() func; matrix_test.go blank-imports each
// interop/servers/<name> package so registration happens as a side effect of
// import, independent of test execution order.
//
// Register panics on a duplicate name: two profiles claiming the same name
// is a harness bug (a copy-pasted profile file), not a runtime condition to
// recover from.
func Register(p Profile) {
	if p.Name == "" {
		panic("harness: profile has empty Name")
	}
	key := strings.ToLower(p.Name)
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[key]; exists {
		panic(fmt.Sprintf("harness: duplicate profile registration for %q", p.Name))
	}
	registry[key] = p
}

// Profiles returns every registered profile, sorted by name for deterministic
// test iteration order.
func Profiles() []Profile {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]Profile, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the profile registered under name (case-insensitive).
func Lookup(name string) (Profile, bool) {
	registryMu.Lock()
	defer registryMu.Unlock()
	p, ok := registry[strings.ToLower(name)]
	return p, ok
}

// Selected returns the registered profiles cfg's Subset and IncludeEmulated
// settings select, in deterministic order.
func Selected(cfg Config) []Profile {
	var out []Profile
	for _, p := range Profiles() {
		if !cfg.Selects(p.Name) {
			continue
		}
		if p.Tier == Tier3 && !cfg.IncludeEmulated {
			continue
		}
		out = append(out, p)
	}
	return out
}

// resetRegistryForTest clears the registry. It exists only for the harness
// package's own unit tests, which must not leak registrations into each
// other or into other packages that later import interop/servers/*.
func resetRegistryForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Profile{}
}
