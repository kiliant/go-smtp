package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

var errPolicyFetch = errors.New("smtpdeliver: MTA-STS policy fetch failed (RFC 8461 §3.3)")

// policyURL is the only location an RFC 8461 §3.2 policy is fetched from.
func policyURL(domain string) string {
	return "https://mta-sts." + domain + "/.well-known/mta-sts.txt"
}

// fetchPolicy performs one hardened RFC 8461 §3.3 fetch for domain and
// returns the body exactly as served. It follows no redirect, accepts only
// status 200 and a text/plain media type, refuses HTTP caching, reads at most
// maxPolicyBody bytes and is bounded by Timeouts.PolicyFetch.
func (d *Deliverer) fetchPolicy(ctx context.Context, domain string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeouts.PolicyFetch)
	defer cancel()

	// A copy of the caller's client: its Transport is shared and treated as
	// immutable, while redirects are refused whatever the caller configured.
	client := *d.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, policyURL(domain), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyFetch, err)
	}
	// RFC 8461 §3.3: HTTP caching MUST NOT be used.
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errPolicyFetch, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxPolicyBody))
		return nil, fmt.Errorf("%w: HTTP status %d", errPolicyFetch, resp.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/plain" {
		return nil, fmt.Errorf("%w: media type %q is not text/plain", errPolicyFetch, resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPolicyBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %w", errPolicyFetch, err)
	}
	if len(body) > maxPolicyBody {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", errPolicyFetch, maxPolicyBody)
	}
	return body, nil
}
