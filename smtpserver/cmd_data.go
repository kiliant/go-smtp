package smtpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

var errMessageTooLarge = errors.New("smtpserver: message exceeds configured SIZE limit")

type sizeEnforcingReader struct {
	reader   io.Reader
	remain   int64
	exceeded bool
	reported bool
}

func (r *sizeEnforcingReader) Read(p []byte) (int, error) {
	if r.exceeded {
		return r.reader.Read(p)
	}
	limit := len(p)
	if int64(limit) > r.remain+1 {
		limit = int(r.remain + 1)
	}
	n, err := r.reader.Read(p[:limit])
	if int64(n) <= r.remain {
		r.remain -= int64(n)
		return n, err
	}
	allowed := int(r.remain)
	r.remain = 0
	r.exceeded = true
	if !r.reported {
		r.reported = true
		if allowed > 0 {
			return allowed, errMessageTooLarge
		}
		return 0, errMessageTooLarge
	}
	return 0, err
}

func (s *commandSession) handleDATA(command smtpwire.Command) (commandAction, error) {
	if command.Argument != "" {
		return s.syntaxReply("DATA")
	}
	if len(s.recipients) == 0 {
		return s.noRecipientsReply()
	}
	if s.binaryMIME {
		if err := s.writeReply(wireReply{code: 503, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, text: "BINARYMIME requires BDAT"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true}, nil
	}
	if err := s.writeReply(wireReply{code: 354, text: "End data with <CRLF>.<CRLF>"}); err != nil {
		return commandAction{}, err
	}
	if err := s.writer.Flush(); err != nil {
		return commandAction{}, err
	}
	dot := smtpwire.NewDotUnstuffReader(s.reader)
	if err := dot.SetDeadline(s.deadline(s.server.timeouts.data)); err != nil {
		return commandAction{}, err
	}
	limited := &sizeEnforcingReader{reader: dot, remain: s.server.maxMessage}
	evaluation, err := s.evaluateContent("DATA", limited)
	if err != nil {
		return commandAction{}, err
	}
	if limited.exceeded {
		evaluation = s.sizeExceededEvaluation("DATA")
	}
	return s.finishContent(evaluation)
}

func (s *commandSession) handleBDAT(command smtpwire.Command) (commandAction, error) {
	framing, err := smtpwire.ParseBDATArgument(command.Argument, s.loop.limits)
	if err != nil {
		if errors.Is(err, smtpwire.ErrBDATSizeOverflow) || errors.Is(err, smtpwire.ErrBDATChunkTooLarge) {
			if err := s.writeReply(wireReply{code: 501, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 2}, text: "Invalid BDAT size"}); err != nil {
				return commandAction{}, err
			}
			return commandAction{synchronizationPoint: true, closeConnection: true}, nil
		}
		return s.syntaxReply("BDAT size")
	}
	if !s.extended || !s.server.chunking {
		return s.notImplementedReply("BDAT")
	}
	if len(s.recipients) == 0 && s.state.phase != phaseFailedBDAT {
		if _, err := s.reader.ReadBDATChunk(io.Discard, framing.Size, s.deadline(s.server.timeouts.data), s.loop.limits); err != nil {
			return commandAction{}, err
		}
		return s.noRecipientsReply()
	}
	if s.state.phase == phaseFailedBDAT {
		if _, err := s.reader.ReadBDATChunk(io.Discard, framing.Size, s.deadline(s.server.timeouts.data), s.loop.limits); err != nil {
			return commandAction{}, err
		}
		if err := s.writeReply(wireReply{code: 503, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, text: "RSET required after failed BDAT"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{}, nil
	}

	if s.lifecycle.spool == nil {
		spool, spoolErr := s.server.spools.newSpool()
		if spoolErr != nil {
			if _, err := s.reader.ReadBDATChunk(io.Discard, framing.Size, s.deadline(s.server.timeouts.data), s.loop.limits); err != nil {
				return commandAction{}, err
			}
			return s.failBDAT(spoolErr)
		}
		s.lifecycle.attachSpool(spool)
	}
	writer := &spoolChunkWriter{spool: s.lifecycle.spool}
	if _, err := s.reader.ReadBDATChunk(writer, framing.Size, s.deadline(s.server.timeouts.data), s.loop.limits); err != nil {
		return commandAction{}, err
	}
	if writer.Err() != nil {
		return s.failBDAT(writer.Err())
	}
	if !framing.Last {
		if err := s.writeReply(wireReply{code: 250, enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, text: "BDAT chunk accepted"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{}, nil
	}
	reader, err := s.lifecycle.spool.Reader()
	if err != nil {
		return s.failBDAT(err)
	}
	evaluation, err := s.evaluateContent("BDAT", reader)
	if err != nil {
		return commandAction{}, err
	}
	return s.finishContent(evaluation)
}

func (s *commandSession) evaluateContent(command string, content io.Reader) (dataEvaluation, error) {
	header, err := smtpwire.FormatReceived(smtpwire.ReceivedOptions{
		From:           s.state.hello,
		By:             s.server.identity,
		Extended:       s.extended || s.state.tls || s.state.authenticated,
		LMTP:           s.state.mode == modeLMTP,
		TLS:            s.state.tls,
		Authenticated:  s.state.authenticated,
		For:            "<" + s.recipients[0] + ">",
		RecipientCount: len(s.recipients),
		Timestamp:      time.Now(),
	})
	if err != nil {
		return dataEvaluation{}, err
	}
	reader := io.MultiReader(strings.NewReader(header), content)
	ctx, cancel := s.commandContext(s.server.timeouts.data)
	evaluation := evaluateDataCall(ctx, s.state.mode, s.recipients, command, reader, func(ctx context.Context, reader io.Reader) (smtp.DataResult, error) {
		return s.backend.Data(ctx, reader, nil)
	})
	cancel()
	if evaluation.defect != nil {
		s.server.reportError(evaluation.defect, s.info)
	}
	if evaluation.cause != nil {
		s.server.reportError(evaluation.cause, s.info)
	}
	return evaluation, nil
}

func (s *commandSession) finishContent(evaluation dataEvaluation) (action commandAction, err error) {
	if evaluation.closeConnection {
		return commandAction{closeConnection: true}, evaluation.cause
	}
	reason := ResetFailed
	event := eventTransactionFailed
	if evaluation.completed {
		reason = ResetCompleted
		event = eventTransactionComplete
	}
	defer func() {
		ctx, cancel := s.commandContext(s.server.timeouts.command)
		s.lifecycle.reset(ctx, reason)
		cancel()
		s.clearTransaction()
		if transitionErr := s.state.transition(event); err == nil && transitionErr != nil {
			err = transitionErr
		}
	}()
	for _, result := range evaluation.result {
		if writeErr := s.writeResult(result); writeErr != nil {
			return commandAction{}, writeErr
		}
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) sizeExceededEvaluation(command string) dataEvaluation {
	count := 1
	if s.state.mode == modeLMTP {
		count = len(s.recipients)
	}
	result := make(smtp.DataResult, count)
	for i := range result {
		result[i] = smtp.RecipientResult{Command: command, Code: 552, Enhanced: smtp.EnhancedCode{Class: 5, Subject: 3, Detail: 4}, Text: "Message size exceeds fixed maximum message size"}
		if s.state.mode == modeLMTP {
			result[i].Recipient = s.recipients[i]
		}
	}
	return dataEvaluation{result: result}
}

func (s *commandSession) noRecipientsReply() (commandAction, error) {
	if err := s.writeReply(wireReply{code: 503, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, text: "No valid recipients"}); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) failBDAT(cause error) (commandAction, error) {
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	s.lifecycle.resetIfOpen(ctx, ResetFailed)
	cancel()
	if err := s.state.transition(eventBDATFailed); err != nil {
		return commandAction{}, err
	}
	code := 451
	enhanced := smtp.EnhancedCode{Class: 4, Subject: 3, Detail: 0}
	text := "Temporary storage failure"
	switch {
	case errors.Is(cause, errSpoolMessageTooLarge):
		code = 552
		enhanced = smtp.EnhancedCode{Class: 5, Subject: 3, Detail: 4}
		text = "Message size exceeds fixed maximum message size"
	case errors.Is(cause, errSpoolTotalExhausted), errors.Is(cause, errSpoolMemoryExhausted), errors.Is(cause, errSpoolConcurrentExhausted):
		code = 452
		enhanced = smtp.EnhancedCode{Class: 4, Subject: 3, Detail: 1}
		text = "Insufficient system storage"
	default:
		s.server.reportError(fmt.Errorf("smtpserver: BDAT spool: %w", cause), s.info)
	}
	if err := s.writeReply(wireReply{code: code, enhanced: enhanced, text: text}); err != nil {
		return commandAction{}, err
	}
	return commandAction{}, nil
}
