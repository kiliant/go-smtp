package smtpclient

import (
	"fmt"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func replyError(command string, reply smtpwire.Reply, parseEnhanced bool) *smtp.Error {
	text := reply.Text
	var code smtp.EnhancedCode
	if parseEnhanced {
		enhanced, rest, ok := smtpwire.ExtractEnhancedCode(text)
		if ok {
			text = rest
			code = smtp.EnhancedCode{
				Class: enhanced.Class, Subject: enhanced.Subject, Detail: enhanced.Detail, Raw: enhanced.Raw,
			}
		}
	}
	return &smtp.Error{
		Code:     reply.Code,
		Enhanced: code,
		Text:     text,
		Command:  command,
	}
}

func (c *connection) enhancedStatusCodes() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.ext[string(smtp.ExtEnhancedStatusCodes)]
	return ok
}

func transportError(command string, err error) *smtp.Error {
	return &smtp.Error{Command: command, Err: err}
}

func unexpectedReply(command string, reply smtpwire.Reply, parseEnhanced bool, want ...int) error {
	for _, code := range want {
		if reply.Code == code {
			return nil
		}
	}
	if reply.Code > 0 {
		return replyError(command, reply, parseEnhanced)
	}
	return transportError(command, fmt.Errorf("smtpclient: missing reply code"))
}
