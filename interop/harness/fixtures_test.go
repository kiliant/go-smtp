package harness

import (
	"bytes"
	"testing"
)

func TestFixturesUniqueNames(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Fixtures {
		if seen[f.Name] {
			t.Errorf("duplicate fixture name %q", f.Name)
		}
		seen[f.Name] = true
		if f.BugClass == "" {
			t.Errorf("fixture %q has no BugClass", f.Name)
		}
	}
}

func TestFixtureByName(t *testing.T) {
	f, ok := FixtureByName("dot-stuffing")
	if !ok {
		t.Fatal("dot-stuffing fixture must exist")
	}
	if !bytes.Contains(f.Body, []byte("\r\n.\r\n")) {
		t.Error("dot-stuffing fixture must contain a lone-dot line")
	}

	if _, ok := FixtureByName("does-not-exist"); ok {
		t.Error("FixtureByName should report false for an unknown name")
	}
}

func TestFixtureDotDotSymmetry(t *testing.T) {
	f, ok := FixtureByName("dot-dot-unstuffing-symmetry")
	if !ok {
		t.Fatal("fixture missing")
	}
	if !bytes.Contains(f.Body, []byte("\r\n..A line")) {
		t.Error("fixture must contain a line starting with two literal dots")
	}
}

func TestFixtureNoTrailingCRLF(t *testing.T) {
	f, ok := FixtureByName("no-trailing-crlf")
	if !ok {
		t.Fatal("fixture missing")
	}
	if bytes.HasSuffix(f.Body, []byte("\r\n")) {
		t.Error("no-trailing-crlf fixture must not end in CRLF")
	}
}

func TestFixtureLineLengthBoundary(t *testing.T) {
	f, ok := FixtureByName("line-length-1000-1001")
	if !ok {
		t.Fatal("fixture missing")
	}
	if !bytes.Contains(f.Body, bytes.Repeat([]byte("a"), 1000)) {
		t.Error("must contain a 1000-octet line")
	}
	if !bytes.Contains(f.Body, bytes.Repeat([]byte("b"), 1001)) {
		t.Error("must contain a 1001-octet line")
	}
}

func TestFixtureBinaryNUL(t *testing.T) {
	f, ok := FixtureByName("binary-with-nul")
	if !ok {
		t.Fatal("fixture missing")
	}
	if !bytes.Contains(f.Body, []byte{0}) {
		t.Error("binary-with-nul fixture must contain a NUL byte")
	}
	if f.RequiresExtension != "BINARYMIME" {
		t.Errorf("RequiresExtension = %q, want BINARYMIME", f.RequiresExtension)
	}
}

func TestFixtureStreamingSize(t *testing.T) {
	f, ok := FixtureByName("streaming-200mib")
	if !ok {
		t.Fatal("fixture missing")
	}
	if len(f.Body) < f.MinBodySize {
		t.Errorf("streaming fixture is %d bytes, want at least %d", len(f.Body), f.MinBodySize)
	}
}

func TestFixtureEightBitBody(t *testing.T) {
	f, ok := FixtureByName("eight-bit-body")
	if !ok {
		t.Fatal("fixture missing")
	}
	hasHighBit := false
	for _, b := range f.Body {
		if b >= 0x80 {
			hasHighBit = true
			break
		}
	}
	if !hasHighBit {
		t.Error("eight-bit-body fixture must contain a byte with the high bit set")
	}
}
