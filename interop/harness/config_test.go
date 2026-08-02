package harness

import (
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv(EnvServerSubset, "")
	cfg := LoadConfig()
	if cfg.StartTimeout != defaultStartTimeout {
		t.Errorf("StartTimeout = %v, want %v", cfg.StartTimeout, defaultStartTimeout)
	}
	if cfg.HealthTimeout != defaultHealthTimeout {
		t.Errorf("HealthTimeout = %v, want %v", cfg.HealthTimeout, defaultHealthTimeout)
	}
	if len(cfg.Subset) != 0 {
		t.Errorf("Subset = %v, want empty", cfg.Subset)
	}
	if cfg.IncludeEmulated {
		t.Error("IncludeEmulated = true by default, want false")
	}
}

func TestLoadConfigSubset(t *testing.T) {
	t.Setenv(EnvServerSubset, " Postfix, mailpit ,,Stalwart")
	cfg := LoadConfig()
	want := []string{"postfix", "mailpit", "stalwart"}
	if len(cfg.Subset) != len(want) {
		t.Fatalf("Subset = %v, want %v", cfg.Subset, want)
	}
	for i, w := range want {
		if cfg.Subset[i] != w {
			t.Errorf("Subset[%d] = %q, want %q", i, cfg.Subset[i], w)
		}
	}
}

func TestConfigSelects(t *testing.T) {
	cfg := Config{}
	if !cfg.Selects("anything") {
		t.Error("empty subset must select everything")
	}
	cfg.Subset = []string{"postfix", "mailpit"}
	if !cfg.Selects("Postfix") {
		t.Error("Selects should be case-insensitive")
	}
	if cfg.Selects("stalwart") {
		t.Error("Selects should reject names outside the subset")
	}
}

func TestConfigStringDoesNotPanic(t *testing.T) {
	cfg := Config{Subset: []string{"a"}, StartTimeout: time.Second}
	if cfg.String() == "" {
		t.Error("String() returned empty")
	}
}
