// Package examples_test contains build-time inventory checks for the runnable
// RFC examples. Each child command is compiled automatically by go test ./....
package examples_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleProgramsPresent(t *testing.T) {
	for _, name := range []string{
		"submission-starttls", "implicit-tls", "partial-rejection",
		"stream-data", "stream-bdat", "lmtp", "dsn", "extra-parameter",
	} {
		if _, err := os.Stat(filepath.Join(name, "main.go")); err != nil {
			t.Errorf("example %s: %v", name, err)
		}
	}
}
