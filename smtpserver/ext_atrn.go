package smtpserver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

const minimumATRNTimeout = 10 * time.Minute

// ATRN connection-ownership design (RFC 2645): ATRN is legal only in the
// authenticated SMTP ready state. The backend first authorizes the requested
// domain set while smtpserver retains the transport. On success the framework
// writes and flushes 250, then transfers exclusive protocol use of that same
// net.Conn to ATRNResult.Takeover. The callback may inject it into smtpclient;
// smtpserver performs no reversed-direction SMTP itself and does not read or
// write concurrently. The original server state machine has ended at that
// point: RSET and QUIT are commands in the new provider-as-client conversation,
// not commands handled by this commandSession. When Takeover returns, on peer
// disconnect, or on shutdown cancellation, the normal connection defer runs
// the original Session.Close exactly once and closes the transport. Takeover
// may use the connection until it returns but may not retain it afterward.

func extensionCommandRule(verb string) (commandRule, bool) {
	if verb == "ATRN" {
		return commandRule{modes: modeSetSMTP, phases: phases(phaseReady)}, true
	}
	return commandRule{}, false
}

func (s *commandSession) executeExtensionCommand(command smtpwire.Command) (commandAction, error) {
	switch strings.ToUpper(command.Verb) {
	case "ATRN":
		return s.handleATRN(command)
	default:
		return commandAction{}, fmt.Errorf("smtpserver: command %s passed legality without a handler", command.Verb)
	}
}

func (s *commandSession) handleATRN(command smtpwire.Command) (commandAction, error) {
	domains, err := parseATRNDomains(command.Argument)
	if err != nil {
		return s.syntaxReply("ATRN domains")
	}
	if !s.state.authenticated {
		if err := s.writeReply(wireReply{code: 530, enhanced: smtp.EnhancedCode{Class: 5, Subject: 7, Detail: 0}, text: "Authentication required"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true}, nil
	}
	if s.backend.ATRN == nil {
		return s.notImplementedReply("ATRN")
	}
	if s.reader.Buffered() != 0 {
		s.server.reportError(errors.New("smtpserver: plaintext bytes were prefetched after ATRN"), s.info)
		if err := s.writeReply(wireReply{code: 554, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 0}, text: "ATRN must not be pipelined"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true, closeConnection: true}, nil
	}

	timeout := s.server.timeouts.command
	if timeout < minimumATRNTimeout {
		timeout = minimumATRNTimeout
	}
	ctx, cancel := s.commandContext(timeout)
	defer cancel()
	result, err := s.backend.ATRN(ctx, append([]string(nil), domains...), nil)
	if err != nil {
		return s.backendReply("ATRN", err, 451, "Unable to process ATRN request now")
	}
	if result == nil || result.Takeover == nil {
		contractErr := errors.New("smtpserver: Session.ATRN returned no takeover callback")
		s.server.reportError(contractErr, s.info)
		return s.backendReply("ATRN", contractErr, 451, "Unable to process ATRN request now")
	}
	if err := s.writeReply(wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, text: "OK, reversing the connection"}); err != nil {
		return commandAction{}, err
	}
	if err := s.writer.Flush(); err != nil {
		return commandAction{}, err
	}
	if err := result.Takeover(ctx, s.transport, nil); err != nil {
		s.server.reportError(fmt.Errorf("smtpserver: ATRN takeover: %w", err), s.info)
	}
	return commandAction{synchronizationPoint: true, closeConnection: true}, nil
}

func parseATRNDomains(argument string) ([]string, error) {
	if argument == "" {
		return nil, nil
	}
	if strings.ContainsAny(argument, " \t\r\n\x00") {
		return nil, errors.New("smtpserver: ATRN domains contain whitespace or framing")
	}
	parts := strings.Split(argument, ",")
	for _, domain := range parts {
		if !validATRNDomain(domain) {
			return nil, fmt.Errorf("smtpserver: invalid ATRN domain %q", domain)
		}
	}
	return parts, nil
}

func validATRNDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}
