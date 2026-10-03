package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// policyRefreshInterval is RFC 8461 §3.3's suggested proactive refresh
	// frequency.
	policyRefreshInterval = 24 * time.Hour
	// policyFetchBackoff is RFC 8461 §3.3's minimum delay between failed
	// fetches for one policy id.
	policyFetchBackoff = 5 * time.Minute
)

var (
	errPolicyCacheLoad    = errors.New("smtpdeliver: MTA-STS policy cache load failed")
	errPolicyCacheStore   = errors.New("smtpdeliver: MTA-STS policy cache store failed")
	errInvalidCachedEntry = errors.New("smtpdeliver: cached MTA-STS policy is invalid")
	errFetchBackoff       = errors.New("smtpdeliver: MTA-STS fetch for this policy id failed within the last five minutes (RFC 8461 §3.3)")
)

// mtastsState is a Deliverer's RFC 8461 coordination: the caller's cache, the
// clock, and the per-id fetch backoff. It holds no policies itself.
type mtastsState struct {
	d     *Deliverer
	cache PolicyCache
	now   func() time.Time

	mu     sync.Mutex
	failed map[string]time.Time // domain + " " + id → last failed fetch
}

func newMTASTSState(d *Deliverer, cache PolicyCache) *mtastsState {
	return &mtastsState{d: d, cache: cache, now: time.Now, failed: map[string]time.Time{}}
}

// mtastsDecision is the RFC 8461 outcome for one destination.
type mtastsDecision struct {
	// result is the PolicyResult recorded on the destination.
	result PolicyResult
	// policy is the policy to apply, enforce or testing; nil when none
	// applies, including for a valid mode "none".
	policy *MTASTSPolicy
	// deferred means the cache could not be read: delivery to the domain is
	// temporarily deferred rather than risk ignoring a policy in force.
	deferred bool
}

// cachedPolicy is a cache entry that reparsed and has not expired.
type cachedPolicy struct {
	entry   PolicyCacheEntry
	policy  MTASTSPolicy
	expires time.Time
}

// evaluate decides the MTA-STS policy for domain, implementing the
// DELIVERY-DESIGN.md §5 state table. force performs live discovery even when
// a valid cached policy is not yet due for refresh. A non-nil error means the
// caller's context ended.
func (m *mtastsState) evaluate(ctx context.Context, domain string, force bool) (mtastsDecision, error) {
	entry, ok, err := m.cache.Load(ctx, &PolicyCacheLoadRequest{Domain: domain})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return mtastsDecision{}, ctxErr
	}
	if err != nil {
		// A load error is not a miss (DELIVERY-DESIGN.md §5).
		cause := fmt.Errorf("%w: %w", errPolicyCacheLoad, err)
		m.d.emit(Event{Kind: EventPolicy, Domain: domain, Cause: cause})
		return mtastsDecision{deferred: true, result: PolicyResult{Kind: PolicyMTASTS, Cause: cause}}, nil
	}

	now := m.now()
	var cached *cachedPolicy
	var cacheCause error
	if ok {
		cached, cacheCause = m.validateCached(domain, entry, now)
	}

	due := force || cached == nil || now.Sub(cached.entry.FetchedAt) >= policyRefreshInterval
	if !due {
		return applied(cached.entry, cached.policy, PolicySourceCache, nil), nil
	}

	live, liveCause, err := m.discover(ctx, domain, now)
	if err != nil {
		return mtastsDecision{}, err
	}
	if live != nil {
		return applied(live.entry, live.policy, PolicySourceFetched, liveCause), nil
	}
	if cached != nil {
		// RFC 8461 §3.3: a valid non-expired cached policy still applies.
		// Its expiry is not extended (DELIVERY-DESIGN.md §5).
		m.d.emit(Event{Kind: EventPolicy, Domain: domain, Cause: liveCause})
		return applied(cached.entry, cached.policy, PolicySourceCache, liveCause), nil
	}
	// No usable live policy and no unexpired cached one: deliver as though
	// the domain has not implemented MTA-STS (RFC 8461 §3.3). An expired
	// policy is never applied.
	cause := errors.Join(liveCause, cacheCause)
	if cause != nil {
		m.d.emit(Event{Kind: EventPolicy, Domain: domain, Cause: cause})
	}
	return mtastsDecision{result: PolicyResult{Kind: PolicyMTASTS, Cause: cause}}, nil
}

// validateCached reparses a loaded entry from its Body; the cache is not a
// parser bypass. It returns nil for an expired entry, and a cause for an
// invalid one, which is then treated as absent.
func (m *mtastsState) validateCached(domain string, entry PolicyCacheEntry, now time.Time) (*cachedPolicy, error) {
	if entry.Domain != domain {
		return nil, fmt.Errorf("%w: entry is for %q, not %q", errInvalidCachedEntry, entry.Domain, domain)
	}
	if !validSTSID(entry.ID) {
		return nil, fmt.Errorf("%w: id %q", errInvalidCachedEntry, entry.ID)
	}
	if entry.FetchedAt.IsZero() || entry.FetchedAt.After(now.Add(time.Minute)) {
		return nil, fmt.Errorf("%w: implausible FetchedAt %v", errInvalidCachedEntry, entry.FetchedAt)
	}
	policy, err := parsePolicy(entry.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidCachedEntry, err)
	}
	// Expiry comes only from the max_age parsed from Body (RFC 8461 §5.1).
	expires := entry.FetchedAt.Add(policy.MaxAge)
	if !now.Before(expires) {
		return nil, nil
	}
	return &cachedPolicy{entry: entry, policy: policy, expires: expires}, nil
}

// discover performs live RFC 8461 §3 discovery: TXT record, then the HTTPS
// policy. It returns the fetched policy, already stored, or nil with the
// reason it could not be obtained. A store failure is returned as the cause
// alongside a usable live policy.
func (m *mtastsState) discover(ctx context.Context, domain string, now time.Time) (*cachedPolicy, error, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, m.d.timeouts.DNS)
	answer, err := m.d.resolver.LookupTXT(lookupCtx, &LookupTXTRequest{Name: "_mta-sts." + domain})
	cancel()
	m.d.emit(Event{Kind: EventLookup, Domain: domain, Cause: err})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("smtpdeliver: MTA-STS TXT lookup for %s: %w", domain, err), nil
	}
	if answer.State != LookupFound {
		return nil, nil, nil
	}
	id, err := parseSTSRecords(answer.Records)
	if err != nil {
		if errors.Is(err, errNoSTSRecord) && countSTSCandidates(answer.Records) == 0 {
			return nil, nil, nil // the domain does not publish MTA-STS
		}
		return nil, err, nil
	}

	key := domain + " " + id
	if m.inBackoff(key, now) {
		return nil, errFetchBackoff, nil
	}
	body, err := m.d.fetchPolicy(ctx, domain)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	var policy MTASTSPolicy
	if err == nil {
		policy, err = parsePolicy(body)
	}
	if err != nil {
		m.recordFailure(key, now)
		return nil, err, nil
	}
	m.clearFailure(key)

	entry := PolicyCacheEntry{Domain: domain, ID: id, Body: body, Policy: policy, FetchedAt: now}
	live := &cachedPolicy{entry: entry, policy: policy, expires: now.Add(policy.MaxAge)}
	var storeCause error
	if err := m.cache.Store(ctx, &PolicyCacheStoreRequest{Entry: cloneEntry(entry)}); err != nil {
		// The live policy still applies to this call (DELIVERY-DESIGN.md §5).
		storeCause = fmt.Errorf("%w: %w", errPolicyCacheStore, err)
		m.d.emit(Event{Kind: EventPolicy, Domain: domain, Cause: storeCause})
	}
	return live, storeCause, nil
}

func countSTSCandidates(records []string) int {
	n := 0
	for _, r := range records {
		if beginsWithSTSVersion(r) {
			n++
		}
	}
	return n
}

func (m *mtastsState) inBackoff(key string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, at := range m.failed {
		if now.Sub(at) >= policyFetchBackoff {
			delete(m.failed, k)
		}
	}
	at, ok := m.failed[key]
	return ok && now.Sub(at) < policyFetchBackoff
}

func (m *mtastsState) recordFailure(key string, now time.Time) {
	m.mu.Lock()
	m.failed[key] = now
	m.mu.Unlock()
}

func (m *mtastsState) clearFailure(key string) {
	m.mu.Lock()
	delete(m.failed, key)
	m.mu.Unlock()
}

// applied builds the decision for a policy that applies: enforce and testing
// constrain or report on attempts, while a valid mode "none" applies nothing.
func applied(entry PolicyCacheEntry, policy MTASTSPolicy, source PolicySource, cause error) mtastsDecision {
	result := PolicyResult{
		Kind:       PolicyMTASTS,
		Mode:       string(policy.Mode),
		Source:     source,
		Applied:    policy.Mode == MTASTSEnforce,
		ValidUntil: entry.FetchedAt.Add(policy.MaxAge),
		Cause:      cause,
	}
	decision := mtastsDecision{result: result}
	if policy.Mode == MTASTSEnforce || policy.Mode == MTASTSTesting {
		p := policy
		p.MX = append([]string(nil), policy.MX...)
		decision.policy = &p
	}
	return decision
}

func cloneEntry(e PolicyCacheEntry) PolicyCacheEntry {
	e.Body = append([]byte(nil), e.Body...)
	e.Policy.MX = append([]string(nil), e.Policy.MX...)
	return e
}
