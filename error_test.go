package smtp

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorImplementsError(t *testing.T) {
	var _ error = (*Error)(nil)
}

func TestErrorString(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "code and text",
			err:  &Error{Code: 550, Text: "no such user here", Command: "RCPT"},
			want: "RCPT: 550 no such user here",
		},
		{
			name: "with enhanced code",
			err: &Error{
				Code:     550,
				Enhanced: ParseEnhancedCode("5.1.1"),
				Text:     "5.1.1 no such user here",
				Command:  "RCPT",
			},
			want: "RCPT: 550 5.1.1 5.1.1 no such user here",
		},
		{
			name: "wrapped cause",
			err:  &Error{Command: "DIAL", Err: errors.New("connection refused")},
			want: "DIAL: connection refused",
		},
		{
			name: "empty",
			err:  &Error{},
			want: "smtp: error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("read tcp: eof")
	err := &Error{Code: 421, Command: "MAIL", Err: cause}

	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false, want true — Unwrap must expose Err")
	}

	var target *Error
	wrapped := fmt.Errorf("dialing: %w", err)
	if !errors.As(wrapped, &target) {
		t.Fatalf("errors.As(wrapped, &target) = false, want true")
	}
	if target.Code != 421 {
		t.Errorf("target.Code = %d, want 421", target.Code)
	}
}

func TestErrorIsTransientIsPermanent(t *testing.T) {
	tests := []struct {
		code                 int
		transient, permanent bool
	}{
		{211, false, false},
		{250, false, false},
		{354, false, false},
		{421, true, false},
		{450, true, false},
		{499, true, false},
		{500, false, true},
		{550, false, true},
		{599, false, true},
		// Servers emit codes no RFC lists (Error.Code is deliberately an
		// int, not an enum, per API-STABILITY.md §5); IsTransient and
		// IsPermanent must degrade rather than panic or misclassify.
		{0, false, false},
		{999, false, false},
	}
	for _, tt := range tests {
		e := &Error{Code: tt.code}
		if got := e.IsTransient(); got != tt.transient {
			t.Errorf("Error{Code:%d}.IsTransient() = %v, want %v", tt.code, got, tt.transient)
		}
		if got := e.IsPermanent(); got != tt.permanent {
			t.Errorf("Error{Code:%d}.IsPermanent() = %v, want %v", tt.code, got, tt.permanent)
		}
	}
}
