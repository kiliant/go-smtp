package smtpdeliver

import "context"

// MemoryPolicyCacheOptions configures NewMemoryPolicyCache. It has no fields
// yet. A nil *MemoryPolicyCacheOptions means defaults (RFC 8461 §5.1).
type MemoryPolicyCacheOptions struct {
	_ struct{}
}

// NewMemoryPolicyCache returns an in-process RFC 8461 policy cache for tests
// and short-lived tools. It is safe for concurrent use.
//
// It loses every policy when the process exits. A restarted sender then has no
// record of enforce policies it saw, which reopens the downgrade window RFC
// 8461 §5.1 caching exists to close. Long-running senders should persist
// PolicyCacheEntry values, Body included, in durable storage through their own
// PolicyCache instead. A nil opts means defaults.
func NewMemoryPolicyCache(opts *MemoryPolicyCacheOptions) PolicyCache {
	_ = opts
	// T27 implements the cache; until then it reports not implemented, which
	// Deliver treats as a cache failure and never as a miss.
	return PolicyCache{
		Load: func(context.Context, *PolicyCacheLoadRequest) (PolicyCacheEntry, bool, error) {
			return PolicyCacheEntry{}, false, errNotImplemented
		},
		Store: func(context.Context, *PolicyCacheStoreRequest) error { return errNotImplemented },
	}
}
