package smtpdeliver

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errDANEVerify = errors.New("smtpdeliver: server certificate matches no usable TLSA record (RFC 7672 §3.2)")

// tlsaMatches reports whether cert matches r's selector and matching type
// (RFC 6698 §2.1.2–2.1.3).
func tlsaMatches(r TLSA, cert *x509.Certificate) bool {
	var data []byte
	switch r.Selector {
	case 0:
		data = cert.Raw
	case 1:
		data = cert.RawSubjectPublicKeyInfo
	default:
		return false
	}
	switch r.MatchingType {
	case 0:
		return bytes.Equal(data, r.Association)
	case 1:
		sum := sha256.Sum256(data)
		return bytes.Equal(sum[:], r.Association)
	case 2:
		sum := sha512.Sum512(data)
		return bytes.Equal(sum[:], r.Association)
	}
	return false
}

// verifyDANE authenticates a presented chain (leaf first) against usable
// TLSA records (RFC 7672 §3). Any one matching record authenticates.
//
// DANE-EE(3) matches the leaf alone: no name checks (§3.2.1) and no expiry
// check (§3.1.1). DANE-TA(2) matches a certificate the server presented
// above the leaf (§3.1.2 requires the server to send it), validates the chain
// from the leaf to it, and checks the leaf's names against refIDs (§3.2.2).
func verifyDANE(chain []*x509.Certificate, records []TLSA, refIDs []string, now time.Time) error {
	if len(chain) == 0 {
		return fmt.Errorf("%w: no certificate presented", errDANEVerify)
	}
	leaf := chain[0]
	var taErr error
	for _, r := range records {
		switch r.Usage {
		case 3:
			if tlsaMatches(r, leaf) {
				return nil
			}
		case 2:
			for i := 1; i < len(chain); i++ {
				if !tlsaMatches(r, chain[i]) {
					continue
				}
				err := verifyTrustAnchorChain(chain, i, now)
				if err == nil {
					err = matchReferenceIDs(leaf, refIDs)
				}
				if err == nil {
					return nil
				}
				taErr = err
			}
		}
	}
	if taErr != nil {
		return fmt.Errorf("%w: %w", errDANEVerify, taErr)
	}
	return errDANEVerify
}

// verifyTrustAnchorChain validates chain[0] up to the trust anchor chain[ta],
// using the certificates in between as intermediates. Validity periods and
// constraints apply as in ordinary path validation; only the root of trust
// differs (RFC 7672 §3.1.2).
func verifyTrustAnchorChain(chain []*x509.Certificate, ta int, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(chain[ta])
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:ta] {
		intermediates.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

// matchReferenceIDs implements RFC 7672 §3.2.3: DNS-IDs only when present,
// otherwise the CN-ID, and a wildcard matches exactly one whole first label.
func matchReferenceIDs(leaf *x509.Certificate, refIDs []string) error {
	presented := leaf.DNSNames
	if len(presented) == 0 && leaf.Subject.CommonName != "" {
		presented = []string{leaf.Subject.CommonName}
	}
	for _, p := range presented {
		for _, ref := range refIDs {
			if certNameMatches(p, ref) {
				return nil
			}
		}
	}
	return fmt.Errorf("certificate names %v match none of %v", presented, refIDs)
}

func certNameMatches(presented, ref string) bool {
	presented = strings.ToLower(strings.TrimSuffix(presented, "."))
	ref = strings.ToLower(strings.TrimSuffix(ref, "."))
	if suffix, wildcard := strings.CutPrefix(presented, "*."); wildcard {
		label, rest, ok := strings.Cut(ref, ".")
		return ok && label != "" && rest == suffix && strings.Contains(suffix, ".")
	}
	return presented != "" && presented == ref
}
