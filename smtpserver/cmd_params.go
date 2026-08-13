package smtpserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

var errCommandParameter = errors.New("smtpserver: invalid command parameter")

type parameterError struct {
	keyword string
	cause   error
}

func (e *parameterError) Error() string {
	if e == nil {
		return errCommandParameter.Error()
	}
	if e.keyword == "" {
		return fmt.Sprintf("%v: %v", errCommandParameter, e.cause)
	}
	return fmt.Sprintf("%v %s: %v", errCommandParameter, e.keyword, e.cause)
}

func (e *parameterError) Unwrap() error { return e.cause }

func (e *parameterError) Is(target error) bool { return target == errCommandParameter }

type mailParameterFeatures struct {
	size       bool
	eightBit   bool
	binaryMIME bool
	smtpUTF8   bool
	auth       bool
}

func parseMailParameters(params []smtpwire.Param, features mailParameterFeatures) (*smtp.MailOptions, error) {
	if len(params) == 0 {
		return nil, nil
	}
	opts := &smtp.MailOptions{}
	seen := make(map[string]bool)
	for _, param := range params {
		keyword := strings.ToUpper(param.Keyword)
		switch keyword {
		case "SIZE":
			if err := uniqueParameter(seen, keyword); err != nil {
				return nil, err
			}
			if !features.size {
				return nil, unavailableParameter(keyword)
			}
			if param.Value == "" || strings.HasPrefix(param.Value, "+") || strings.HasPrefix(param.Value, "-") {
				return nil, invalidParameter(keyword, errors.New("value must be an unsigned decimal integer"))
			}
			size, err := strconv.ParseInt(param.Value, 10, 64)
			if err != nil || size < 0 {
				return nil, invalidParameter(keyword, errors.New("value must be an unsigned decimal integer"))
			}
			transport := ensureTransport(opts)
			transport.Size = &size
		case "BODY":
			if err := uniqueParameter(seen, keyword); err != nil {
				return nil, err
			}
			body := smtp.BodyType(strings.ToUpper(param.Value))
			switch body {
			case smtp.BodyType7Bit, smtp.BodyType8BitMIME:
				if !features.eightBit {
					return nil, unavailableParameter(keyword)
				}
			case smtp.BodyTypeBinaryMIME:
				if !features.binaryMIME {
					return nil, unavailableParameter(keyword)
				}
			default:
				return nil, invalidParameter(keyword, errors.New("value must be 7BIT, 8BITMIME, or BINARYMIME"))
			}
			ensureTransport(opts).Body = body
		case "SMTPUTF8":
			if err := uniqueParameter(seen, keyword); err != nil {
				return nil, err
			}
			if !features.smtpUTF8 {
				return nil, unavailableParameter(keyword)
			}
			if param.Value != "" {
				return nil, invalidParameter(keyword, errors.New("parameter must not have a value"))
			}
			ensureTransport(opts).SMTPUTF8 = true
		case "AUTH":
			if err := uniqueParameter(seen, keyword); err != nil {
				return nil, err
			}
			if !features.auth {
				return nil, unavailableParameter(keyword)
			}
			if param.Value == "" {
				return nil, invalidParameter(keyword, errors.New("xtext value is required"))
			}
			decoded, err := smtpwire.DecodeXtext(param.Value)
			if err != nil {
				return nil, invalidParameter(keyword, err)
			}
			opts.Auth = decoded
			opts.AuthOriginal = &smtp.Param{Keyword: param.Keyword, Value: param.Value}
		default:
			opts.Extra = append(opts.Extra, smtp.Param{Keyword: param.Keyword, Value: param.Value})
		}
	}
	return opts, nil
}

func parseRcptParameters(params []smtpwire.Param) *smtp.RcptOptions {
	if len(params) == 0 {
		return nil
	}
	opts := &smtp.RcptOptions{Extra: make([]smtp.Param, 0, len(params))}
	for _, param := range params {
		opts.Extra = append(opts.Extra, smtp.Param{Keyword: param.Keyword, Value: param.Value})
	}
	return opts
}

func ensureTransport(opts *smtp.MailOptions) *smtp.TransportOptions {
	if opts.Transport == nil {
		opts.Transport = &smtp.TransportOptions{}
	}
	return opts.Transport
}

func uniqueParameter(seen map[string]bool, keyword string) error {
	if seen[keyword] {
		return invalidParameter(keyword, errors.New("parameter appears more than once"))
	}
	seen[keyword] = true
	return nil
}

func unavailableParameter(keyword string) error {
	return invalidParameter(keyword, errors.New("extension was not advertised"))
}

func invalidParameter(keyword string, cause error) error {
	return &parameterError{keyword: keyword, cause: cause}
}
