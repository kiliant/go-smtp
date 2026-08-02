package smtpwire

import "testing"

func TestExtractEnhancedCode(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantCode EnhancedCode
		wantRest string
		wantOK   bool
	}{
		{
			name:     "simple",
			in:       "2.1.5 Recipient OK",
			wantCode: EnhancedCode{Class: 2, Subject: 1, Detail: 5, Raw: "2.1.5"},
			wantRest: "Recipient OK",
			wantOK:   true,
		},
		{
			name:     "class 4",
			in:       "4.7.0 Try again later",
			wantCode: EnhancedCode{Class: 4, Subject: 7, Detail: 0, Raw: "4.7.0"},
			wantRest: "Try again later",
			wantOK:   true,
		},
		{
			name:     "class 5",
			in:       "5.5.1 Invalid command",
			wantCode: EnhancedCode{Class: 5, Subject: 5, Detail: 1, Raw: "5.5.1"},
			wantRest: "Invalid command",
			wantOK:   true,
		},
		{
			name:     "multi-digit subject and detail",
			in:       "5.123.456 wild but grammatically valid",
			wantCode: EnhancedCode{Class: 5, Subject: 123, Detail: 456, Raw: "5.123.456"},
			wantRest: "wild but grammatically valid",
			wantOK:   true,
		},
		{
			name:     "code with no trailing text",
			in:       "2.0.0",
			wantCode: EnhancedCode{Class: 2, Subject: 0, Detail: 0, Raw: "2.0.0"},
			wantRest: "",
			wantOK:   true,
		},
		{
			name:     "no code present",
			in:       "Recipient OK",
			wantCode: EnhancedCode{},
			wantRest: "Recipient OK",
			wantOK:   false,
		},
		{
			name:     "invalid class digit",
			in:       "1.1.1 not a valid class",
			wantCode: EnhancedCode{},
			wantRest: "1.1.1 not a valid class",
			wantOK:   false,
		},
		{
			name:     "fourth digit in subject rejected",
			in:       "5.1234.1 too many digits",
			wantCode: EnhancedCode{},
			wantRest: "5.1234.1 too many digits",
			wantOK:   false,
		},
		{
			name:     "fourth digit in detail rejected",
			in:       "5.1.1234 too many digits",
			wantCode: EnhancedCode{},
			wantRest: "5.1.1234 too many digits",
			wantOK:   false,
		},
		{
			name:     "missing detail",
			in:       "5.1. missing detail",
			wantCode: EnhancedCode{},
			wantRest: "5.1. missing detail",
			wantOK:   false,
		},
		{
			name:     "empty string",
			in:       "",
			wantCode: EnhancedCode{},
			wantRest: "",
			wantOK:   false,
		},
		{
			name:     "code glued to text with no separating space",
			in:       "2.1.5Recipient",
			wantCode: EnhancedCode{},
			wantRest: "2.1.5Recipient",
			wantOK:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, rest, ok := ExtractEnhancedCode(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if code != tt.wantCode {
				t.Errorf("code = %+v, want %+v", code, tt.wantCode)
			}
			if rest != tt.wantRest {
				t.Errorf("rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}
