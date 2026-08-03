package smtpclient

import (
	"strings"
	"testing"
)

// FuzzAuthAdvertisement keeps RFC 4954's AUTH advertisement parsing total, in
// both the standard "AUTH mech mech" form and the historic "AUTH=mech" form.
// The text is server-controlled and feeds mechanism selection, so the target
// asserts the property that makes selection sound rather than only the absence
// of a panic: a mechanism is never chosen unless the server advertised it.
func FuzzAuthAdvertisement(f *testing.F) {
	f.Add("AUTH", "PLAIN LOGIN", "")
	f.Add("AUTH", "SCRAM-SHA-256 SCRAM-SHA-256-PLUS PLAIN", "")
	f.Add("AUTH", "=PLAIN", "")
	f.Add("AUTH=LOGIN", "PLAIN", "")
	f.Add("AUTH", "", "PLAIN")
	f.Add("AUTH", "plain login", "LOGIN,PLAIN")
	f.Add("AUTH", "  \t PLAIN \t ", "")
	f.Add("SIZE", "10485760", "PLAIN")

	f.Fuzz(func(t *testing.T, keyword, params, preferred string) {
		if len(keyword) > 4<<10 || len(params) > 64<<10 || len(preferred) > 4<<10 {
			t.Skip()
		}
		// One entry only: authAdvertisement scans the table for the legacy
		// AUTH= form, and Go map iteration order would make a multi-entry
		// input non-reproducible — a fuzz finding that cannot be replayed is
		// worse than no finding.
		advertised, ok := authAdvertisement(map[string]string{keyword: params})
		if !ok {
			return
		}
		available := authMechanisms(advertised)
		for name := range available {
			if name != strings.ToUpper(name) {
				t.Fatalf("advertised mechanism %q is not upper-cased", name)
			}
		}

		var want []string
		if preferred != "" {
			want = strings.Split(preferred, ",")
		}
		selected, err := selectMechanism(want, available)
		if err != nil {
			return
		}
		if !available[selected] {
			t.Fatalf("selected %q, which the server did not advertise (%v)", selected, available)
		}
		if selected != strings.ToUpper(selected) {
			t.Fatalf("selected %q, want an upper-cased mechanism name", selected)
		}
	})
}
