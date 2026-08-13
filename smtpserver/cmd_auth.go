package smtpserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpsasl"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func (s *commandSession) handleAUTH(command smtpwire.Command) (commandAction, error) {
	fields := strings.Fields(command.Argument)
	if len(fields) < 1 || len(fields) > 2 {
		return s.syntaxReply("AUTH mechanism and initial response")
	}
	mechanism := strings.ToUpper(fields[0])
	if !containsFold(s.authMechanisms(), mechanism) {
		if err := s.writeReply(wireReply{code: 504, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 4}, text: "Unsupported authentication mechanism"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true}, nil
	}
	binding, err := channelBinding(s.ctx, s.tlsState, mechanism)
	if err != nil {
		return s.backendReply("AUTH", err, 454, "Temporary authentication failure")
	}
	responder, err := smtpsasl.NewResponder(mechanism, smtpsasl.ResponderConfig{Hostname: s.server.identity, ChannelBinding: binding})
	if err != nil {
		return s.backendReply("AUTH", err, 454, "Temporary authentication failure")
	}
	var initial []byte
	if len(fields) == 2 {
		initial, err = decodeSASLResponse(fields[1])
		if err != nil {
			return s.syntaxReply("AUTH initial response")
		}
	}
	step, err := responder.Start(initial)
	if err != nil {
		return s.authExchangeError(err)
	}
	var authoritative *AuthResult
	for {
		if step.Verification != nil {
			verification, result, verifyErr := s.verifyAUTH(step.Verification)
			if verifyErr != nil {
				return s.backendReply("AUTH", verifyErr, 454, "Temporary authentication failure")
			}
			authoritative = result
			step, err = responder.Continue(verification)
			if err != nil {
				return s.authExchangeError(err)
			}
			continue
		}
		if step.Challenge != nil {
			challenge := base64.StdEncoding.EncodeToString(step.Challenge)
			if err := s.writeReply(wireReply{code: 334, text: challenge}); err != nil {
				return commandAction{}, err
			}
			if err := s.writer.Flush(); err != nil {
				return commandAction{}, err
			}
			line, err := s.reader.ReadSASLResponse(s.deadline(s.server.timeouts.command), s.loop.limits)
			if err != nil {
				if errors.Is(err, smtpwire.ErrCommandLineTooLong) {
					if err := s.writeReply(wireReply{code: 500, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 6}, text: "Authentication exchange line is too long"}); err != nil {
						return commandAction{}, err
					}
					return commandAction{synchronizationPoint: true, closeConnection: true}, nil
				}
				return commandAction{}, err
			}
			if s.server.trace != nil {
				s.server.trace(smtp.TraceEvent{Direction: smtp.TraceReceived, Line: "<redacted>"})
			}
			response, err := decodeSASLResponse(line)
			if line == "*" {
				response = []byte("*")
			}
			if err != nil {
				return s.authExchangeError(err)
			}
			step, err = responder.Next(response)
			if err != nil {
				return s.authExchangeError(err)
			}
			continue
		}
		if !step.Done {
			return commandAction{}, errors.New("smtpserver: SASL responder made no progress")
		}
		if !step.Accepted {
			return s.authFailure(authoritative)
		}
		commitCtx, commitCancel := s.commandContext(s.server.timeouts.command)
		result, err := finalizeAuthentication(commitCtx, s.backend, authoritative, nil, true)
		commitCancel()
		if err != nil {
			s.server.reportError(err, s.info)
			if err := s.writeReply(wireReply{code: 454, enhanced: smtp.EnhancedCode{Class: 4, Subject: 7, Detail: 0}, text: "Temporary authentication failure"}); err != nil {
				return commandAction{}, err
			}
			return commandAction{synchronizationPoint: true}, nil
		}
		_ = result
		if err := s.state.transition(eventAuthenticated); err != nil {
			return commandAction{}, err
		}
		if err := s.writeReply(wireReply{code: 235, enhanced: smtp.EnhancedCode{Class: 2, Subject: 7, Detail: 0}, text: "Authentication successful"}); err != nil {
			return commandAction{}, err
		}
		return commandAction{synchronizationPoint: true}, nil
	}
}

func (s *commandSession) verifyAUTH(request *smtpsasl.VerificationRequest) (smtpsasl.Verification, *AuthResult, error) {
	ctx, cancel := s.commandContext(s.server.timeouts.command)
	defer cancel()
	switch request.Kind {
	case smtpsasl.VerifyCredentials:
		if s.backend.Authenticate == nil {
			return smtpsasl.Verification{}, nil, errBackendAuthContract
		}
		credentials := &Credentials{
			Mechanism:        request.Mechanism,
			AuthenticationID: request.Credentials.AuthenticationID,
			AuthorizationID:  request.Credentials.AuthorizationID,
			Password:         request.Credentials.Password,
			Token:            request.Credentials.Token,
			TLS:              s.tlsState.get(),
		}
		result, err := s.backend.Authenticate(ctx, credentials, nil)
		if err == nil && result == nil {
			err = errBackendAuthContract
		}
		return authVerification(result), result, err
	case smtpsasl.VerifyChallengeResponse:
		if s.backend.ChallengeResponse == nil {
			return smtpsasl.Verification{}, nil, errBackendAuthContract
		}
		result, err := s.backend.ChallengeResponse(ctx, &Challenge{Mechanism: request.Mechanism, Challenge: request.Challenge, Response: request.Response}, nil)
		if err == nil && result == nil {
			err = errBackendAuthContract
		}
		return authVerification(result), result, err
	case smtpsasl.VerifySCRAMKeys:
		if s.backend.SCRAMCredentials == nil {
			return smtpsasl.Verification{}, nil, errBackendAuthContract
		}
		credentials := &Credentials{Mechanism: request.Mechanism, AuthenticationID: request.Credentials.AuthenticationID, AuthorizationID: request.Credentials.AuthorizationID, TLS: s.tlsState.get()}
		keys, err := s.backend.SCRAMCredentials(ctx, credentials, nil)
		if err != nil {
			return smtpsasl.Verification{}, nil, err
		}
		if keys == nil {
			return smtpsasl.Verification{}, nil, errBackendAuthContract
		}
		result := &keys.Result
		verification := authVerification(result)
		if verification.Accepted {
			verification.Keys = &smtpsasl.SCRAMKeys{Salt: append([]byte(nil), keys.Salt...), Iterations: keys.Iterations, StoredKey: append([]byte(nil), keys.StoredKey...), ServerKey: append([]byte(nil), keys.ServerKey...)}
		}
		return verification, result, nil
	default:
		return smtpsasl.Verification{}, nil, errBackendAuthContract
	}
}

func authVerification(result *AuthResult) smtpsasl.Verification {
	if result == nil {
		return smtpsasl.Verification{}
	}
	verification := smtpsasl.Verification{Accepted: result.Failure == nil}
	if result.Failure != nil && result.Failure.OAuth != nil {
		payload, err := json.Marshal(map[string]string{
			"status":               result.Failure.OAuth.Status,
			"scope":                result.Failure.OAuth.Scope,
			"openid-configuration": result.Failure.OAuth.OpenIDConfiguration,
		})
		if err == nil {
			verification.FailureChallenge = payload
		}
	}
	return verification
}

func (s *commandSession) authFailure(result *AuthResult) (commandAction, error) {
	reply := wireReply{code: 535, enhanced: smtp.EnhancedCode{Class: 5, Subject: 7, Detail: 8}, text: "Authentication credentials invalid"}
	if result != nil && result.Failure != nil && result.Failure.Err != nil {
		reply = errorReply("AUTH", result.Failure.Err, 535, reply.text)
	}
	if err := s.writeReply(reply); err != nil {
		return commandAction{}, err
	}
	return commandAction{synchronizationPoint: true}, nil
}

func (s *commandSession) authExchangeError(err error) (commandAction, error) {
	if errors.Is(err, smtpsasl.ErrResponderAborted) || errors.Is(err, smtpsasl.ErrResponderPayload) {
		if writeErr := s.writeReply(wireReply{code: 501, enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 2}, text: "Authentication exchange cancelled or malformed"}); writeErr != nil {
			return commandAction{}, writeErr
		}
		return commandAction{synchronizationPoint: true}, nil
	}
	return commandAction{}, err
}

func decodeSASLResponse(value string) ([]byte, error) {
	if value == "=" || value == "" {
		return []byte{}, nil
	}
	if value == "*" {
		return []byte("*"), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) > 16<<10 {
		return nil, errors.New("smtpserver: invalid SASL response")
	}
	return decoded, nil
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
