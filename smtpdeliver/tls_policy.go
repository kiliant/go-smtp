package smtpdeliver

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	errMTASTSMXMismatch     = errors.New("smtpdeliver: MX host matches no mx pattern of the MTA-STS policy (RFC 8461 §4.1)")
	errMTASTSCertificate    = errors.New("smtpdeliver: certificate does not validate for the MX host (RFC 8461 §4.2)")
	errRequireTLSMXNotValid = errors.New("smtpdeliver: REQUIRETLS needs a DNSSEC-validated MX or one matching an MTA-STS policy (RFC 8689 §4.2.1)")
	errRequireTLSAuth       = errors.New("smtpdeliver: REQUIRETLS needs an authenticated server certificate (RFC 8689 §4.2.1)")
	errTLSRequired          = errors.New("smtpdeliver: TLS is required but the server did not offer STARTTLS")
)

// tlsInput is everything that decides transport security for one attempt.
type tlsInput struct {
	domain string
	step   routeStep
	// sts is the destination's MTA-STS decision; nil when MTA-STS is
	// disabled.
	sts *mtastsDecision
	// dane is the MX host's RFC 7672 evaluation.
	dane daneResult
	// requireTLS is the caller's RFC 8689 REQUIRETLS request.
	requireTLS bool
}

// tlsAttempt is the transport-security plan for one address step,
// DELIVERY-DESIGN.md §6. It is used once.
type tlsAttempt struct {
	// skip, when non-nil, forbids contacting the candidate at all.
	skip error
	// required means the session must be TLS-protected: a server that does
	// not offer STARTTLS, or a failed handshake, makes the candidate
	// unreachable. Opportunistic attempts may proceed in cleartext only when
	// STARTTLS was not offered; a failed advertised handshake is never
	// retried in cleartext on the same address.
	required bool
	// serverName is the SNI and certificate identity: the TLSA base domain
	// when DANE applies (RFC 7672 §8.1), otherwise the MX host name.
	serverName string
	// config is the STARTTLS configuration. Verification happens in its
	// VerifyConnection, which records outcomes in report.
	config *tls.Config
	report *tlsReport
}

// tlsReport collects the per-attempt PolicyResults, including outcomes
// decided during the handshake.
type tlsReport struct {
	mu   sync.Mutex
	sts  *PolicyResult
	dane *PolicyResult
}

// policies returns the attempt's policy evaluations, MTA-STS first.
func (r *tlsReport) policies() []PolicyResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []PolicyResult
	if r.sts != nil {
		out = append(out, *r.sts)
	}
	if r.dane != nil {
		out = append(out, *r.dane)
	}
	return out
}

func (r *tlsReport) failSTS(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sts != nil && r.sts.Cause == nil {
		r.sts.Cause = err
	}
}

func (r *tlsReport) failDANE(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dane != nil && r.dane.Cause == nil {
		r.dane.Cause = err
	}
}

// noSTARTTLS records that the server did not offer STARTTLS and reports
// whether the attempt may continue in cleartext.
func (a *tlsAttempt) noSTARTTLS() error {
	a.report.failSTS(errTLSRequired)
	if a.required {
		return errTLSRequired
	}
	return nil
}

// planTLS decides transport security for one attempt (DELIVERY-DESIGN.md §6).
// Requirements from every applicable policy intersect: DANE, MTA-STS enforce,
// REQUIRETLS and mandatory DANE each add constraints and none relaxes
// another, so an MTA-STS-valid certificate never rescues a failed DANE match
// (RFC 8461 §2).
func (d *Deliverer) planTLS(in tlsInput) *tlsAttempt {
	mx := in.step.host.name
	a := &tlsAttempt{serverName: mx, report: &tlsReport{}}

	var sts *MTASTSPolicy
	if in.sts != nil {
		r := in.sts.result
		a.report.sts = &r
		a.report.sts.Cause = nil // per-attempt outcome; the destination keeps its own
		sts = in.sts.policy
	}
	if d.dane {
		a.report.dane = &PolicyResult{Kind: PolicyDANE, Mode: string(d.daneMode), Source: PolicySourceDNS, Applied: in.dane.state != daneNone && d.daneMode != DANEAudit}
	}

	// Pre-connection checks: any of these makes the candidate unreachable.
	if in.dane.err != nil {
		a.report.failDANE(in.dane.err)
		a.skip = in.dane.err
		return a
	}
	if d.dane && d.daneMode == DANEMandatory {
		switch {
		case in.step.host.security != DNSSECSecure:
			a.skip = errDANEInsecureMX
		case in.dane.state != daneUsable:
			a.skip = errDANEMandatory
		}
		if a.skip != nil {
			a.report.failDANE(a.skip)
			return a
		}
	}
	stsMatches := sts != nil && policyMatchesMX(*sts, mx)
	if sts != nil && !stsMatches {
		err := fmt.Errorf("%w: %s", errMTASTSMXMismatch, mx)
		a.report.failSTS(err)
		if sts.Mode == MTASTSEnforce {
			a.skip = err
			return a
		}
	}
	if in.requireTLS && in.step.host.security != DNSSECSecure && !stsMatches {
		a.skip = errRequireTLSMXNotValid
		return a
	}

	enforce := sts != nil && sts.Mode == MTASTSEnforce
	daneEnforced := in.dane.state != daneNone && d.daneMode != DANEAudit
	a.required = daneEnforced || enforce || in.requireTLS
	if in.dane.state == daneUsable {
		a.serverName = in.dane.baseDomain
	}

	cfg := &tls.Config{}
	if d.tlsConfig != nil {
		cfg = d.tlsConfig.Clone()
	}
	callerVerify := cfg.VerifyConnection
	roots := cfg.RootCAs
	cfg.ServerName = a.serverName
	// Verification is ours, in VerifyConnection, so that opportunistic TLS
	// can proceed unauthenticated (RFC 7435) while policies that require
	// authentication cannot be disabled by the caller's configuration.
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if err := a.verify(cs, in, sts, enforce, roots); err != nil {
			return err
		}
		if callerVerify != nil {
			return callerVerify(cs)
		}
		return nil
	}
	a.config = cfg
	return a
}

// verify runs inside the handshake. It returns an error only for a failure
// that must abort the attempt; advisory failures are recorded.
func (a *tlsAttempt) verify(cs tls.ConnectionState, in tlsInput, sts *MTASTSPolicy, enforce bool, roots *x509.CertPool) error {
	now := time.Now()
	daneOK := false
	if in.dane.state == daneUsable {
		if err := verifyDANE(cs.PeerCertificates, in.dane.records, in.dane.refIDs, now); err != nil {
			a.report.failDANE(err)
			if a.report.dane.Applied { // not audit mode
				return err
			}
		} else {
			daneOK = true
		}
	}

	needPKIX := enforce || (in.requireTLS && !daneOK)
	checkPKIX := needPKIX || (sts != nil && sts.Mode == MTASTSTesting)
	if !checkPKIX {
		return nil
	}
	if err := verifyPKIX(cs.PeerCertificates, in.step.host.name, roots, now); err != nil {
		if sts != nil {
			a.report.failSTS(fmt.Errorf("%w: %w", errMTASTSCertificate, err))
		}
		if needPKIX {
			if in.requireTLS && !enforce {
				return fmt.Errorf("%w: %w", errRequireTLSAuth, err)
			}
			return fmt.Errorf("%w: %w", errMTASTSCertificate, err)
		}
	}
	return nil
}

// verifyPKIX validates the presented chain for host with Web PKI roots
// (RFC 8461 §4.2, RFC 6125). nil roots selects the system pool.
func verifyPKIX(chain []*x509.Certificate, host string, roots *x509.CertPool, now time.Time) error {
	if len(chain) == 0 {
		return errors.New("no certificate presented")
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{
		DNSName:       host,
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}
