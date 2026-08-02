package harness

import (
	"context"
	"testing"
)

func testProfile(name string, tier Tier) Profile {
	return Profile{
		Name: name,
		Tier: tier,
		Run:  RunConfig{Image: "example/" + name},
		Ports: []Port{
			{Container: 25, Kind: "smtp"},
		},
		NewSink: func(ctx context.Context, h *Handle) (Sink, error) { return nil, nil },
	}
}

func TestRegisterAndLookup(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(testProfile("testserver-a", Tier1))
	p, ok := Lookup("TestServer-A")
	if !ok {
		t.Fatal("Lookup should be case-insensitive")
	}
	if p.Name != "testserver-a" {
		t.Errorf("Name = %q", p.Name)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(testProfile("dup", Tier1))
	defer func() {
		if recover() == nil {
			t.Error("Register should panic on a duplicate name")
		}
	}()
	Register(testProfile("dup", Tier1))
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	defer func() {
		if recover() == nil {
			t.Error("Register should panic on an empty Name")
		}
	}()
	Register(Profile{})
}

func TestProfilesSortedDeterministic(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(testProfile("zeta", Tier1))
	Register(testProfile("alpha", Tier1))
	Register(testProfile("mid", Tier2))

	names := func() []string {
		var out []string
		for _, p := range Profiles() {
			out = append(out, p.Name)
		}
		return out
	}
	first := names()
	second := names()
	want := []string{"alpha", "mid", "zeta"}
	for i, w := range want {
		if first[i] != w || second[i] != w {
			t.Fatalf("Profiles() order = %v, want %v (repeat call: %v)", first, want, second)
		}
	}
}

func TestSelectedFiltersSubsetAndTier(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register(testProfile("native", Tier1))
	Register(testProfile("emulated", Tier3))

	cfg := Config{}
	sel := Selected(cfg)
	if len(sel) != 1 || sel[0].Name != "native" {
		t.Fatalf("Selected without IncludeEmulated = %v, want only [native]", sel)
	}

	cfg.IncludeEmulated = true
	sel = Selected(cfg)
	if len(sel) != 2 {
		t.Fatalf("Selected with IncludeEmulated = %v, want both profiles", sel)
	}

	cfg.Subset = []string{"emulated"}
	sel = Selected(cfg)
	if len(sel) != 1 || sel[0].Name != "emulated" {
		t.Fatalf("Selected with subset = %v, want only [emulated]", sel)
	}
}
