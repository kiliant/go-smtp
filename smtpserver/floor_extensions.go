package smtpserver

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

const (
	backendAuthenticate backendFeatureSet = 1 << iota
	backendChallengeResponse
	backendSCRAM
)

func (s *commandSession) capabilities() []smtpwire.Extension {
	auth := s.authMechanisms()
	descriptors := []capabilityDescriptor{
		{keyword: smtp.ExtPipelining, modes: modeSetBoth},
		{keyword: smtp.ExtSize, modes: modeSetBoth, params: func(capabilityContext) string { return strconv.FormatInt(s.server.maxMessage, 10) }},
		{keyword: smtp.Ext8BitMIME, modes: modeSetBoth},
		{keyword: smtp.ExtEnhancedStatusCodes, modes: modeSetBoth},
		{keyword: smtp.ExtStartTLS, modes: modeSetBoth, requiresTLS: tlsBefore, available: func(capabilityContext) bool { return s.server.tlsConfig != nil }},
		{keyword: smtp.ExtAuth, modes: modeSetBoth, params: func(capabilityContext) string { return strings.Join(auth, " ") }, available: func(ctx capabilityContext) bool { return len(auth) != 0 && !ctx.authenticated }},
		{keyword: smtp.ExtSMTPUTF8, modes: modeSetBoth},
		{keyword: smtp.ExtChunking, modes: modeSetBoth, available: func(capabilityContext) bool { return s.server.chunking }},
		{keyword: smtp.ExtBinaryMIME, modes: modeSetBoth, available: func(capabilityContext) bool { return s.server.binaryMIME && s.server.chunking }},
	}
	return computeCapabilities(capabilityContext{
		mode:          s.state.mode,
		tls:           s.state.tls,
		authenticated: s.state.authenticated,
		backend:       s.backendFeatures(),
	}, descriptors)
}

func (s *commandSession) backendFeatures() backendFeatureSet {
	return backendFeatures(s.backend)
}

func (s *commandSession) authMechanisms() []string {
	if !s.extended {
		return nil
	}
	configured := s.server.authBefore
	if s.state.tls {
		configured = s.server.authAfter
	}
	state := s.tlsState.get()
	return availableAuthMechanisms(configured, s.backendFeatures(), s.state.tls, state != nil && len(state.PeerCertificates) != 0)
}

func availableAuthMechanisms(configured []string, features backendFeatureSet, tls, clientCertificate bool) []string {
	result := make([]string, 0, len(configured))
	for _, configuredName := range configured {
		name := strings.ToUpper(configuredName)
		switch name {
		case "PLAIN", "LOGIN", "OAUTHBEARER", "XOAUTH2":
			if !features.contains(backendAuthenticate) {
				continue
			}
		case "EXTERNAL":
			if !features.contains(backendAuthenticate) || !clientCertificate {
				continue
			}
		case "CRAM-MD5":
			if !features.contains(backendChallengeResponse) {
				continue
			}
		case "SCRAM-SHA-1", "SCRAM-SHA-256":
			if !features.contains(backendSCRAM) {
				continue
			}
		case "SCRAM-SHA-1-PLUS", "SCRAM-SHA-256-PLUS":
			if !features.contains(backendSCRAM) || !tls {
				continue
			}
		default:
			continue
		}
		result = append(result, name)
	}
	return result
}

func (s *Server) authReachable(session *Session, tlsState *connectionTLSState) bool {
	features := backendFeatures(session)
	state := tlsState.get()
	if state != nil {
		return len(availableAuthMechanisms(s.authAfter, features, true, len(state.PeerCertificates) != 0)) != 0
	}
	if len(availableAuthMechanisms(s.authBefore, features, false, false)) != 0 {
		return true
	}
	if s.tlsConfig == nil {
		return false
	}
	// EXTERNAL is intentionally excluded before a handshake. A TLS config may
	// choose ClientAuth dynamically through GetConfigForClient, and even a
	// requesting config cannot promise that a peer supplies a certificate.
	// Re-checking the actual post-handshake mechanism set in handleHello keeps
	// RequireAuth from producing a permanently unusable connection.
	return len(availableAuthMechanisms(s.authAfter, features, true, false)) != 0 || containsFold(s.authAfter, "EXTERNAL") && features.contains(backendAuthenticate)
}

func backendFeatures(session *Session) backendFeatureSet {
	var features backendFeatureSet
	if session.Authenticate != nil {
		features |= backendAuthenticate
	}
	if session.ChallengeResponse != nil {
		features |= backendChallengeResponse
	}
	if session.SCRAMCredentials != nil {
		features |= backendSCRAM
	}
	return features
}

func (s *commandSession) mailParameterFeatures() mailParameterFeatures {
	return mailParameterFeatures{
		size:       s.extended,
		eightBit:   s.extended,
		binaryMIME: s.server.binaryMIME && s.server.chunking,
		smtpUTF8:   s.extended,
		auth:       len(s.authMechanisms()) != 0,
	}
}

func (s *commandSession) handleSTARTTLS(command smtpwire.Command) (commandAction, error) {
	if command.Argument != "" {
		return s.syntaxReply("STARTTLS")
	}
	if !s.extended {
		return s.notImplementedReply("STARTTLS")
	}
	if s.server.tlsConfig == nil {
		return s.notImplementedReply("STARTTLS")
	}
	if err := s.writeReply(wireReply{code: 220, enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, text: "Ready to start TLS"}); err != nil {
		return commandAction{}, err
	}
	if err := s.writer.Flush(); err != nil {
		return commandAction{}, err
	}
	handshakeCtx, cancel := s.commandContext(s.server.timeouts.command)
	tlsConn, err := handshakeTLS(handshakeCtx, s.raw, s.server.tlsConfig, s.reader, func(discarded int) {
		s.server.reportError(fmt.Errorf("smtpserver: discarded %d plaintext bytes prefetched after STARTTLS", discarded), s.info)
	})
	cancel()
	if err != nil {
		return commandAction{}, err
	}
	state := tlsConn.ConnectionState()
	s.tlsState.set(state)
	resetCtx, resetCancel := s.commandContext(s.server.timeouts.command)
	s.lifecycle.reset(resetCtx, ResetStartTLS)
	resetCancel()
	if err := s.state.transition(eventStartTLS); err != nil {
		return commandAction{}, err
	}
	s.extended = false
	s.clearTransaction()
	s.transport = tlsConn
	s.reader = smtpwire.NewLineReader(tlsConn)
	s.writer = bufio.NewWriter(tlsConn)
	s.loop.reader = s.reader
	s.loop.writer = s.writer
	return commandAction{}, nil
}

func channelBinding(ctx context.Context, state *connectionTLSState, mechanism string) ([]byte, error) {
	_ = ctx
	if !strings.HasSuffix(strings.ToUpper(mechanism), "-PLUS") {
		return nil, nil
	}
	tlsState := state.get()
	if tlsState == nil {
		return nil, fmt.Errorf("smtpserver: %s requires TLS channel binding", mechanism)
	}
	return tlsState.ExportKeyingMaterial("EXPORTER-Channel-Binding", nil, 32)
}
