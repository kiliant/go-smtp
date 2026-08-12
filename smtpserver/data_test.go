package smtpserver

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp"
)

func TestEvaluateDataCallDrainsBeforePreservingEarlyRejection(t *testing.T) {
	reader := &observedReader{Reader: strings.NewReader("complete message")}
	evaluation := evaluateDataCall(
		context.Background(),
		modeSMTP,
		[]string{"user@example.test"},
		"DATA",
		reader,
		func(_ context.Context, content io.Reader) (smtp.DataResult, error) {
			one := make([]byte, 1)
			if _, err := content.Read(one); err != nil {
				t.Fatal(err)
			}
			return smtp.DataResult{{Code: 550, Enhanced: smtp.EnhancedCode{Class: 5, Subject: 7, Detail: 1}, Text: "rejected"}}, nil
		},
	)
	if !reader.eof {
		t.Fatal("reader was not drained to EOF")
	}
	if !evaluation.completed || evaluation.defect != nil || len(evaluation.result) != 1 || evaluation.result[0].Code != 550 {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

func TestEvaluateDataCallReplacesEarlySuccess(t *testing.T) {
	evaluation := evaluateDataCall(
		context.Background(),
		modeSMTP,
		[]string{"user@example.test"},
		"BDAT",
		strings.NewReader("complete message"),
		func(context.Context, io.Reader) (smtp.DataResult, error) {
			return smtp.DataResult{{Code: 250, Enhanced: smtp.EnhancedCode{Class: 2}, Text: "accepted"}}, nil
		},
	)
	if evaluation.completed || !errors.Is(evaluation.defect, errBackendDataContract) {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	if got := evaluation.result[0]; got.Code != 451 || got.Enhanced.String() != "4.3.0" {
		t.Fatalf("replacement = %+v", got)
	}
}

func TestEvaluateDataCallDrainFailureClosesWithoutReply(t *testing.T) {
	wantErr := errors.New("transport failed")
	evaluation := evaluateDataCall(
		context.Background(),
		modeSMTP,
		[]string{"user@example.test"},
		"DATA",
		&terminalErrorReader{err: wantErr},
		func(context.Context, io.Reader) (smtp.DataResult, error) {
			return smtp.DataResult{{Code: 550}}, nil
		},
	)
	if !evaluation.closeConnection || len(evaluation.result) != 0 || !errors.Is(evaluation.cause, wantErr) {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

func TestValidateDataOutcomeInternalFailureUsesModeCardinality(t *testing.T) {
	wantErr := errors.New("database unavailable")
	recipients := []string{"same@example.test", "same@example.test"}
	evaluation := validateDataOutcome(modeLMTP, recipients, "DATA", nil, wantErr)
	if evaluation.completed || evaluation.defect != nil || !errors.Is(evaluation.cause, wantErr) {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	if len(evaluation.result) != 2 || evaluation.result[0].Recipient != recipients[0] || evaluation.result[1].Recipient != recipients[1] {
		t.Fatalf("result = %+v", evaluation.result)
	}
}

func TestValidateDataOutcomeRejectsAmbiguousAndWrongCardinality(t *testing.T) {
	recipients := []string{"one@example.test", "two@example.test"}
	tests := []struct {
		name   string
		result smtp.DataResult
		err    error
	}{
		{name: "both result and error", result: smtp.DataResult{{Recipient: recipients[0], Code: 250}}, err: errors.New("also failed")},
		{name: "too few LMTP results", result: smtp.DataResult{{Recipient: recipients[0], Code: 250}}},
		{name: "wrong LMTP order", result: smtp.DataResult{{Recipient: recipients[1], Code: 250}, {Recipient: recipients[0], Code: 250}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := validateDataOutcome(modeLMTP, recipients, "DATA", test.result, test.err)
			if evaluation.completed || !errors.Is(evaluation.defect, errBackendDataContract) {
				t.Fatalf("evaluation = %+v", evaluation)
			}
			if len(evaluation.result) != len(recipients) {
				t.Fatalf("temporary result length = %d", len(evaluation.result))
			}
		})
	}
}

func TestEvaluateDataCallFullyConsumedSuccess(t *testing.T) {
	evaluation := evaluateDataCall(
		context.Background(),
		modeSMTP,
		[]string{"user@example.test"},
		"DATA",
		strings.NewReader("message"),
		func(_ context.Context, content io.Reader) (smtp.DataResult, error) {
			if _, err := io.Copy(io.Discard, content); err != nil {
				t.Fatal(err)
			}
			return smtp.DataResult{{Code: 250, Enhanced: smtp.EnhancedCode{Class: 2}, Text: "accepted"}}, nil
		},
	)
	if !evaluation.completed || evaluation.defect != nil || evaluation.result[0].Code != 250 {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

func TestValidateDataOutcomeRepairsEnhancedMismatchAndReportsDefect(t *testing.T) {
	result := smtp.DataResult{{
		Code:     550,
		Enhanced: smtp.ParseEnhancedCode("4.7.1"),
		Text:     "policy rejection",
	}}
	evaluation := validateDataOutcome(modeSMTP, []string{"user@example.test"}, "DATA", result, nil)
	if !evaluation.completed || !errors.Is(evaluation.defect, errBackendDataContract) {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	if got := evaluation.result[0].Enhanced.String(); got != "5.0.0" {
		t.Fatalf("enhanced = %q, want 5.0.0", got)
	}
}

type observedReader struct {
	io.Reader
	eof bool
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.eof = true
	}
	return n, err
}

type terminalErrorReader struct {
	err error
}

func (r *terminalErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}
