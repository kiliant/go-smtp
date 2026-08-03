package smtpclient

import (
	"strconv"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

// advertisingClient builds a Client whose only populated state is the EHLO
// extension table. Both parsers under test reach the server-supplied text
// through Client.Extension and touch nothing else, so a fake server would add
// a goroutine and a net.Pipe per execution for no additional coverage.
func advertisingClient(keyword smtp.Extension, params string) *Client {
	return &Client{conn: &connection{ext: map[string]string{string(keyword): params}}}
}

// FuzzDeliverByAdvertisement keeps the RFC 2852 by-mode "R" minimum-interval
// parser total. The advertisement is server-controlled text, so a malformed
// or hostile DELIVERBY parameter must produce an error rather than a panic —
// and must never yield a BY= parameter of the wrong shape on the wire.
func FuzzDeliverByAdvertisement(f *testing.F) {
	f.Add("", int64(30))
	f.Add("0", int64(30))
	f.Add("240", int64(30))
	f.Add("-1", int64(30))
	f.Add("abc", int64(30))
	f.Add("9223372036854775808", int64(30))
	f.Add("1 2", int64(999999999))

	f.Fuzz(func(t *testing.T, params string, seconds int64) {
		if len(params) > 64<<10 {
			t.Skip()
		}
		c := advertisingClient(smtp.ExtDeliverBy, params)

		// The raw value exercises the by-time range check that runs before the
		// advertisement is consulted.
		_, _ = c.deliverByParam(&smtp.DeliverByOptions{Seconds: seconds, Mode: "R"})

		// Mode "R" rejects out-of-range by-time before reading the
		// advertisement, so an in-range value is what actually reaches the
		// parser this target exists for.
		inRange := seconds % 999999999
		if inRange < 0 {
			inRange = -inRange
		}
		if inRange == 0 {
			inRange = 1
		}
		param, err := c.deliverByParam(&smtp.DeliverByOptions{Seconds: inRange, Mode: "R"})
		if err != nil {
			return
		}
		if param.Keyword != "BY" {
			t.Fatalf("keyword = %q, want BY", param.Keyword)
		}
		// by-value = by-time ";" by-mode. A malformed advertisement must not
		// be able to alter the parameter the client emits.
		want := strconv.FormatInt(inRange, 10) + ";R"
		if param.Value != want {
			t.Fatalf("value = %q, want %q", param.Value, want)
		}
	})
}

// FuzzFutureReleaseAdvertisement keeps the RFC 4865 "max-future-release-interval
// SP max-future-release-date-time" parser total. Both fields come from the
// server, and the date-time bounds a value this client then puts on the wire,
// so a hostile advertisement must never produce a malformed HOLDUNTIL.
func FuzzFutureReleaseAdvertisement(f *testing.F) {
	f.Add("86400 2026-08-03T00:00:00Z", int64(3600), "")
	f.Add("86400 2026-08-03T00:00:00Z", int64(0), "2026-08-02T12:00:00Z")
	f.Add("", int64(3600), "")
	f.Add("86400", int64(3600), "")
	f.Add("0 2026-08-03T00:00:00Z", int64(3600), "")
	f.Add("abc def", int64(3600), "")
	f.Add("86400 not-a-date", int64(0), "2026-08-02T12:00:00Z")
	f.Add("86400 2026-08-03T00:00:00Z 3", int64(3600), "")

	f.Fuzz(func(t *testing.T, params string, holdFor int64, holdUntil string) {
		if len(params) > 64<<10 || len(holdUntil) > 64<<10 {
			t.Skip()
		}
		// Exactly one of the two is required, and that check runs before the
		// advertisement is read. Normalising here keeps both branches
		// reachable instead of failing most executions at the first gate.
		if holdUntil != "" {
			holdFor = 0
		} else if holdFor == 0 {
			holdFor = 1
		}
		c := advertisingClient(smtp.ExtFutureRelease, params)

		param, err := c.futureReleaseParam(&smtp.FutureReleaseOptions{HoldForSeconds: holdFor, HoldUntil: holdUntil})
		if err != nil {
			return
		}
		switch param.Keyword {
		case "HOLDFOR":
			n, convErr := strconv.ParseInt(param.Value, 10, 64)
			if convErr != nil || n < 1 {
				t.Fatalf("HOLDFOR value = %q, want a positive integer", param.Value)
			}
		case "HOLDUNTIL":
			if _, convErr := time.Parse(time.RFC3339, param.Value); convErr != nil {
				t.Fatalf("HOLDUNTIL value = %q, want RFC 3339: %v", param.Value, convErr)
			}
		default:
			t.Fatalf("keyword = %q, want HOLDFOR or HOLDUNTIL", param.Keyword)
		}
	})
}
