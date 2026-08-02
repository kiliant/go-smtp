package harness

import (
	"strings"
	"testing"
)

// TestContainerNameUnique guards the exact regression documented in
// docs/INTEROP.md and T06: two names generated within the same process in
// the same wall-clock second must never collide, because that made podman
// run fail outright in the sibling repo.
func TestContainerNameUnique(t *testing.T) {
	seen := make(map[string]bool)
	for range 10000 {
		name := ContainerName("postfix")
		if seen[name] {
			t.Fatalf("duplicate container name generated: %s", name)
		}
		seen[name] = true
	}
}

func TestContainerNameEmbedsProfile(t *testing.T) {
	name := ContainerName("mailpit")
	if !strings.Contains(name, "mailpit") {
		t.Errorf("ContainerName(%q) = %q, want it to embed the profile name", "mailpit", name)
	}
}
