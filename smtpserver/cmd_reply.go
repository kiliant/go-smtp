package smtpserver

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

type wireReply struct {
	code     int
	enhanced smtp.EnhancedCode
	text     string
	lines    []string
	context  smtpwire.ReplyContext
}

func encodeWireReply(writer io.Writer, reply wireReply) error {
	var enhanced *smtpwire.EnhancedCode
	if reply.enhanced.String() != "" {
		enhanced = &smtpwire.EnhancedCode{Class: reply.enhanced.Class, Subject: reply.enhanced.Subject, Detail: reply.enhanced.Detail}
	}
	return smtpwire.EncodeReply(writer, smtpwire.Reply{Code: reply.code, Text: reply.text, Lines: reply.lines}, smtpwire.ReplyOptions{
		Enhanced: enhanced,
		Context:  reply.context,
	})
}

func errorReply(command string, err error, fallbackCode int, fallbackText string) wireReply {
	var protocol *smtp.Error
	if errors.As(err, &protocol) && protocol.Code >= 200 {
		enhanced, repaired := normalizeEnhancedCode(protocol.Code, protocol.Enhanced)
		_ = repaired
		text := protocol.Text
		if text == "" {
			text = fallbackText
		}
		return wireReply{code: protocol.Code, enhanced: enhanced, text: text}
	}
	return wireReply{code: fallbackCode, enhanced: genericEnhanced(fallbackCode), text: fallbackText}
}

func genericEnhanced(code int) smtp.EnhancedCode {
	switch code / 100 {
	case 2, 4, 5:
		return smtp.EnhancedCode{Class: code / 100, Subject: 0, Detail: 0}
	default:
		return smtp.EnhancedCode{}
	}
}

func (s *commandSession) writeReply(reply wireReply) error {
	if err := s.transport.SetWriteDeadline(s.deadline(s.server.timeouts.command)); err != nil {
		return err
	}
	if err := encodeWireReply(s.writer, reply); err != nil {
		return err
	}
	if s.server.trace != nil {
		line := strconv.Itoa(reply.code) + " " + reply.text
		if len(reply.lines) != 0 {
			line = strconv.Itoa(reply.code) + " " + strings.Join(reply.lines, "\n")
		}
		s.server.trace(smtp.TraceEvent{Direction: smtp.TraceSent, Line: line})
	}
	return nil
}

func (s *commandSession) writeResult(result smtp.RecipientResult) error {
	normalized, repaired := normalizeRecipientResult(result)
	if repaired {
		s.server.reportError(fmt.Errorf("%w: enhanced status class disagrees with reply code", errBackendDataContract), s.info)
	}
	text := normalized.Text
	if text == "" {
		text = "OK"
	}
	return s.writeReply(wireReply{code: normalized.Code, enhanced: normalized.Enhanced, text: text})
}
