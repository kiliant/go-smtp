package smtp

import "testing"

func TestParamValuelessParameter(t *testing.T) {
	p := Param{Keyword: "REQUIRETLS"}
	if p.Value != "" {
		t.Errorf("Param{Keyword: %q}.Value = %q, want empty for a valueless parameter", p.Keyword, p.Value)
	}
}

// TestParamValueTypesAreOpen stresses the named future extension from the
// task brief directly: BODY=BINARYMIME registered after BODY=8BITMIME, and
// RFC 6533 registering the "utf-8" ORCPT= address-type after "rfc822". Both
// must be constructible even though this file only names the values known
// when it was written, because these types are string-backed rather than
// enums (API-STABILITY.md §1b).
func TestParamValueTypesAreOpen(t *testing.T) {
	// A value this package has never named still round-trips: nothing
	// about the type is a closed switch.
	future := BodyType("QUANTUMMIME")
	if string(future) != "QUANTUMMIME" {
		t.Errorf("BodyType(%q) round-trip failed", future)
	}

	// The historical case this type must absorb without a breaking
	// change: BINARYMIME (RFC 3030) arrived after 8BITMIME (RFC 6152).
	values := []BodyType{BodyType7Bit, BodyType8BitMIME, BodyTypeBinaryMIME}
	for _, v := range values {
		if v == "" {
			t.Errorf("BodyType constant is empty")
		}
	}

	// RFC 6533 added "utf-8" to ORCPT= after RFC 3461 registered
	// "rfc822"; an unregistered future address-type must also
	// construct cleanly.
	futureAddrType := ORcptAddressType("mailto-plus")
	if string(futureAddrType) != "mailto-plus" {
		t.Errorf("ORcptAddressType(%q) round-trip failed", futureAddrType)
	}
	if ORcptAddressTypeRFC822 == ORcptAddressTypeUTF8 {
		t.Errorf("ORcptAddressTypeRFC822 and ORcptAddressTypeUTF8 must be distinct")
	}
}

func TestDSNNotifyCombination(t *testing.T) {
	// RFC 3461 §4.1 allows combining NOTIFY values in a comma-separated
	// list; DSNNotify is string-backed so a caller composes this itself.
	combined := DSNNotify(string(DSNNotifyFailure) + "," + string(DSNNotifyDelay))
	if combined != "FAILURE,DELAY" {
		t.Errorf("combined DSNNotify = %q, want %q", combined, "FAILURE,DELAY")
	}
}

func TestMTPriorityRoundTrips(t *testing.T) {
	// RFC 6710 §3.1 values are signed decimal integers, but MTPriority is
	// string-backed, so out-of-range or oddly formatted values still
	// round-trip unchanged.
	for _, s := range []string{"0", "-9", "9", "007"} {
		p := MTPriority(s)
		if string(p) != s {
			t.Errorf("MTPriority(%q) round-trip failed", s)
		}
	}
}
