//go:build interop_emulated

package harness

// emulatedBuildTag is true only when the interop_emulated build tag is
// present, gating Tier 3 (non-native-architecture) profiles the same way
// docs/INTEROP.md documents: "go test -tags='interop interop_emulated'".
const emulatedBuildTag = true
