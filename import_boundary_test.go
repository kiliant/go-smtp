package smtp

import (
	"go/build"
	"strings"
	"testing"
)

// TestNoModuleImports enforces T02's hard requirement 1 and the CLAUDE.md
// layering rule: package smtp imports nothing from this module. It is the
// shared vocabulary that smtpclient, a future smtpdeliver package and a
// future server framework all depend on (docs/ARCHITECTURE.md); if it
// imported back from any of them, that would be a cycle the moment they
// tried to import smtp.
//
// go/build.Package.Imports lists only the production (non-test) source
// files' imports, which is exactly the constraint at stake: this test file
// itself may not import from the module either way, since it doesn't need
// to.
func TestNoModuleImports(t *testing.T) {
	const modulePath = "github.com/kiliant/go-smtp"

	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("build.ImportDir(\".\"): %v", err)
	}
	if pkg.Name != "smtp" {
		t.Fatalf("build.ImportDir(\".\") found package %q, want \"smtp\"", pkg.Name)
	}

	for _, imp := range pkg.Imports {
		if imp == modulePath || strings.HasPrefix(imp, modulePath+"/") {
			t.Errorf("package smtp imports %q from this module; package smtp must import nothing from %s", imp, modulePath)
		}
	}
}
