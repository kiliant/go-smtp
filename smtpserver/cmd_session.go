package smtpserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

type commandSession struct {
	server    *Server
	ctx       context.Context
	raw       net.Conn
	transport net.Conn
	tlsState  *connectionTLSState
	info      *ConnInfo
	backend   *Session
	lifecycle *sessionLifecycle
	state     protocolState
	reader    *smtpwire.LineReader
	writer    *bufio.Writer
	loop      commandLoop

	extended   bool
	smtpUTF8   bool
	binaryMIME bool
	recipients []string
}

func (s *commandSession) deadline(timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if outer, ok := s.ctx.Deadline(); ok && outer.Before(deadline) {
		return outer
	}
	return deadline
}

func (s *commandSession) commandContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithDeadline(s.ctx, s.deadline(timeout))
}

func (s *commandSession) close() {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), s.server.timeouts.command)
	defer cancel()
	s.lifecycle.close(cleanupCtx)
	_ = s.state.transition(eventClose)
}

func (s *commandSession) execute(ctx context.Context, command smtpwire.Command, _ *smtpwire.LineReader, _ *bufio.Writer) (commandAction, error) {
	verb := strings.ToUpper(command.Verb)
	s.traceCommand(verb, command.Argument)
	legality := s.state.legality(verb)
	switch legality {
	case commandUnknown:
		if err := s.writeReply(wireReply{code: 500, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, text: "Command unrecognized"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true}, nil
	case commandWrongMode:
		if err := s.writeReply(wireReply{code: 500, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, text: "Command unavailable in this protocol mode"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: synchronizationPoint(verb)}, nil
	case commandWrongState:
		if err := s.writeReply(wireReply{code: 503, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, text: "Bad sequence of commands"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: synchronizationPoint(verb)}, nil
	}

	switch verb {
	case "HELO", "EHLO", "LHLO":
		return s.handleHello(command)
	case "MAIL":
		return s.handleMail(command)
	case "RCPT":
		return s.handleRcpt(command)
	case "DATA":
		return s.handleDATA(command)
	case "BDAT":
		return s.handleBDAT(command)
	case "RSET":
		return s.handleRSET(command)
	case "NOOP":
		return s.handleNOOP(command)
	case "QUIT":
		return s.handleQUIT(command)
	case "VRFY":
		return s.handleVRFY(command)
	case "EXPN":
		return s.handleEXPN(command)
	case "HELP":
		return s.handleHELP(command)
	case "ETRN":
		return s.handleETRN(command)
	case "STARTTLS":
		return s.handleSTARTTLS(command)
	case "AUTH":
		return s.handleAUTH(command)
	default:
		return s.executeExtensionCommand(command)
	}
}

func synchronizationPoint(verb string) bool {
	switch verb {
	case "MAIL", "RCPT", "RSET", "BDAT":
		return false
	default:
		return true
	}
}

func (s *commandSession) traceCommand(verb, argument string) {
	if s.server.trace == nil {
		return
	}
	line := verb
	if argument != "" {
		if verb == "AUTH" {
			name, _, _ := strings.Cut(argument, " ")
			line += " " + name + " <redacted>"
		} else {
			line += " " + argument
		}
	}
	s.server.trace(smtp.TraceEvent{Direction: smtp.TraceReceived, Line: line})
}

func (s *commandSession) handleHello(command smtpwire.Command) (commandAction, error) {
	hello, err := smtpwire.ParseHelloCommand(command)
	if err != nil || !validGreetingIdentity(hello.Domain) {
		return s.syntaxReply("HELO/EHLO/LHLO argument")
	}
	if s.lifecycle.transactionOpen {
		ctx, cancel := s.commandContext(s.server.timeouts.command)
		s.lifecycle.reset(ctx, ResetExplicit)
		cancel()
	}
	if err := s.state.transition(eventHello); err != nil {
		return commandAction{}, err
	}
	s.state.hello = hello.Domain
	s.extended = hello.Verb != "HELO"
	s.smtpUTF8 = false
	s.binaryMIME = false
	s.recipients = nil
	if s.server.requireAuth && s.state.tls && !s.state.authenticated && len(s.authMechanisms()) == 0 {
		s.server.reportError(fmt.Errorf("%w: RequireAuth has no mechanism available after TLS", errBackendAuthContract), s.info)
		if err := s.writeReply(wireReply{code: 421, enhanced: smtp.EnhancedCode{Class: 4, Subject: 3, Detail: 0}, text: "Service not available"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true, closeConnection: true}, nil
	}
	if hello.Verb == "HELO" {
		if err := s.writeReply(wireReply{code: 250, text: s.server.identity, context: smtpwire.ReplyContextHello}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true}, nil
	}
	if err := s.transport.SetWriteDeadline(s.deadline(s.server.timeouts.command)); err != nil {
		return commandAction{}, err
	}
	extensions := s.capabilities()
	if err := smtpwire.EncodeEHLOReply(s.writer, smtpwire.EHLOReply{Domain: s.server.identity, Extensions: extensions}); err != nil {
		return commandAction{}, err
	}
	if s.server.trace != nil {
		s.server.trace(smtp.TraceEvent{Direction: smtp.TraceSent, Line: "250 " + s.server.identity})
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) handleMail(command smtpwire.Command) (commandAction, error) {
	if s.server.requireTLS && !s.state.tls {
		return s.policyReply("TLS is required")
	}
	if s.server.requireAuth && !s.state.authenticated {
		return s.policyReply("Authentication is required")
	}
	path, err := smtpwire.ParseReversePath(command.Argument, smtpwire.PathOptions{SMTPUTF8: true})
	if err != nil {
		return s.syntaxReply("MAIL FROM path")
	}
	if !s.extended && len(path.Params) != 0 {
		return s.syntaxReply(path.Params[0].Keyword)
	}
	features := s.mailParameterFeatures()
	params, err := parseMailParameters(path.Params, features)
	if err != nil {
		var parameter *parameterError
		if errors.As(err, &parameter) {
			return s.syntaxReply(parameter.keyword)
		}
		return s.syntaxReply("MAIL parameter")
	}
	if err := validateExtensionMailPath(path.Mailbox, params); err != nil {
		var parameter *parameterError
		if errors.As(err, &parameter) {
			return s.syntaxReply(parameter.keyword)
		}
		return s.syntaxReply("MAIL parameter")
	}
	requestedUTF8 := params != nil && params.Transport != nil && params.Transport.SMTPUTF8
	if hasNonASCII(path.Mailbox) && !requestedUTF8 {
		return s.syntaxReply("SMTPUTF8")
	}
	if s.lifecycle.transactionOpen {
		ctx, cancel := s.commandContext(s.server.timeouts.command)
		s.lifecycle.reset(ctx, ResetNewMail)
		cancel()
		if err := s.state.transition(eventTransactionFailed); err != nil {
			return commandAction{}, err
		}
		s.clearTransaction()
	}
	if params != nil && params.Transport != nil && params.Transport.Size != nil && *params.Transport.Size > s.server.maxMessage {
		if err := s.writeReply(wireReply{code: 552, enhanced: smtp.EnhancedCode{Class: 5, Subject: 3, Detail: 4}, text: "Message size exceeds fixed maximum message size"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{}, nil
	}
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	err = s.lifecycle.mail(ctx, path.Mailbox, params, nil)
	cancel()
	if err != nil {
		if s.state.phase == phaseMail || s.state.phase == phaseRecipients {
			_ = s.state.transition(eventTransactionFailed)
		}
		s.clearTransaction()
		return s.backendReply("MAIL", err, 451, "Temporary server failure")
	}
	if err := s.state.transition(eventMailAccepted); err != nil {
		return commandAction{}, err
	}
	s.smtpUTF8 = requestedUTF8
	s.binaryMIME = params != nil && params.Transport != nil && params.Transport.Body == smtp.BodyTypeBinaryMIME
	s.recipients = nil
	if err := s.writeReply(wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 1, Detail: 0}, text: "Sender OK"}); err != nil {
		return commandAction{}, err
	}
	return commandAction{}, nil
}

func (s *commandSession) handleRcpt(command smtpwire.Command) (commandAction, error) {
	path, err := smtpwire.ParseForwardPath(command.Argument, smtpwire.PathOptions{SMTPUTF8: s.smtpUTF8})
	if err != nil {
		return s.syntaxReply("RCPT TO path")
	}
	if !s.extended && len(path.Params) != 0 {
		return s.syntaxReply(path.Params[0].Keyword)
	}
	if len(s.recipients) >= s.server.maxRcpt {
		if err := s.writeReply(wireReply{code: 452, enhanced: smtp.EnhancedCode{Class: 4, Subject: 5, Detail: 3}, text: "Too many recipients"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{}, nil
	}
	params, err := parseRcptParameters(path.Params, s.parameterExtensionMap())
	if err != nil {
		var parameter *parameterError
		if errors.As(err, &parameter) {
			return s.syntaxReply(parameter.keyword)
		}
		return s.syntaxReply("RCPT parameter")
	}
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	replyOptions := &RcptOptions{}
	err = s.backend.Rcpt(ctx, path.Mailbox, params, replyOptions)
	cancel()
	if err != nil {
		return s.backendReply("RCPT", err, 451, "Temporary server failure")
	}
	if err := validateRcptSuccessLines(replyOptions.SuccessLines); err != nil {
		s.server.reportError(err, s.info)
		return s.backendReply("RCPT", err, 451, "Temporary server failure")
	}
	s.recipients = append(s.recipients, path.Mailbox)
	if s.state.phase == phaseMail {
		if err := s.state.transition(eventRecipientAccepted); err != nil {
			return commandAction{}, err
		}
	}
	reply := wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 1, Detail: 5}, text: "Recipient OK"}
	if len(replyOptions.SuccessLines) != 0 {
		reply.enhanced = smtp.EnhancedCode{}
		reply.text = ""
		reply.lines = append([]string{"Recipient OK"}, replyOptions.SuccessLines...)
	}
	if err := s.writeReply(reply); err != nil {
		return commandAction{}, err
	}
	return commandAction{}, nil
}

func validateRcptSuccessLines(lines []string) error {
	for _, line := range lines {
		if line == "" || strings.ContainsAny(line, "\r\n\x00") {
			return errors.New("smtpserver: Session.Rcpt returned an invalid success continuation line")
		}
	}
	return nil
}

func (s *commandSession) handleRSET(command smtpwire.Command) (commandAction, error) {
	if command.Argument != "" {
		return s.syntaxReply("RSET")
	}
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	s.lifecycle.resetIfOpen(ctx, ResetExplicit)
	cancel()
	if err := s.state.transition(eventReset); err != nil {
		return commandAction{}, err
	}
	s.clearTransaction()
	if err := s.writeReply(wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, text: "Reset state"}); err != nil {
		return commandAction{}, err
	}
	return commandAction{}, nil
}

func (s *commandSession) handleNOOP(command smtpwire.Command) (commandAction, error) {
	if err := s.writeReply(wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, text: "OK"}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) handleQUIT(command smtpwire.Command) (commandAction, error) {
	if command.Argument != "" {
		return s.syntaxReply("QUIT")
	}
	if err := s.writeReply(wireReply{code: 221, enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, text: s.server.identity + " closing connection"}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true, closeConnection: true}, nil
}

func (s *commandSession) handleVRFY(command smtpwire.Command) (commandAction, error) {
	if command.Argument == "" {
		return s.syntaxReply("VRFY address")
	}
	if s.backend.Verify == nil {
		if err := s.writeReply(wireReply{code: 252, enhanced: smtp.EnhancedCode{Class: 2, Subject: 5, Detail: 2}, text: "Cannot VRFY user, but will accept message"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true}, nil
	}
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	result, err := s.backend.Verify(ctx, command.Argument, nil)
	cancel()
	if err != nil {
		return s.backendReply("VRFY", err, 451, "Temporary server failure")
	}
	if result == "" {
		result = command.Argument
	}
	if err := s.writeReply(wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 1, Detail: 5}, text: result}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) handleEXPN(command smtpwire.Command) (commandAction, error) {
	if command.Argument == "" {
		return s.syntaxReply("EXPN list")
	}
	if s.backend.Expand == nil {
		return s.notImplementedReply("EXPN")
	}
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	addresses, err := s.backend.Expand(ctx, command.Argument, nil)
	cancel()
	if err != nil {
		return s.backendReply("EXPN", err, 451, "Temporary server failure")
	}
	if len(addresses) == 0 {
		addresses = []string{"No members"}
	}
	if err := s.writeReply(wireReply{code: 250, lines: addresses}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) handleHELP(command smtpwire.Command) (commandAction, error) {
	if s.backend.Help == nil {
		return s.notImplementedReply("HELP")
	}
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	text, err := s.backend.Help(ctx, command.Argument, nil)
	cancel()
	if err != nil {
		return s.backendReply("HELP", err, 451, "Temporary server failure")
	}
	if err := s.writeReply(wireReply{code: 214, text: text}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) handleETRN(command smtpwire.Command) (commandAction, error) {
	if command.Argument == "" {
		return s.syntaxReply("ETRN domain")
	}
	if s.backend.ETRN == nil {
		return s.notImplementedReply("ETRN")
	}
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	err := s.backend.ETRN(ctx, command.Argument, nil)
	cancel()
	if err != nil {
		return s.backendReply("ETRN", err, 451, "Temporary server failure")
	}
	if err := s.writeReply(wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, text: "Queuing started"}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) syntaxReply(name string) (commandAction, error) {
	if err := s.writeReply(wireReply{code: 501, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 2}, text: "Invalid " + name}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) policyReply(text string) (commandAction, error) {
	if err := s.writeReply(wireReply{code: 530, enhanced: smtp.EnhancedCode{Class: 5, Subject: 7, Detail: 0}, text: text}); err != nil {
		return commandAction{}, err
	}
	return commandAction{}, nil
}

func (s *commandSession) notImplementedReply(command string) (commandAction, error) {
	if err := s.writeReply(wireReply{code: 502, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, text: command + " not implemented"}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) backendReply(command string, err error, fallbackCode int, fallbackText string) (commandAction, error) {
	var protocol *smtp.Error
	if !errors.As(err, &protocol) {
		s.server.reportError(fmt.Errorf("smtpserver: %s backend: %w", command, err), s.info)
	}
	if writeErr := s.writeReply(errorReply(command, err, fallbackCode, fallbackText)); writeErr != nil {
		return commandAction{}, writeErr
	}
	return commandAction{synchronizationPoint: synchronizationPoint(command)}, nil
}

func (s *commandSession) clearTransaction() {
	s.smtpUTF8 = false
	s.binaryMIME = false
	s.recipients = nil
}

func hasNonASCII(value string) bool {
	for i := range len(value) {
		if value[i] >= 0x80 {
			return true
		}
	}
	return false
}
