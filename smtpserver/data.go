package smtpserver

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/kiliant/go-smtp"
)

var errBackendDataContract = errors.New("smtpserver: backend Data contract violation")

type trackedDataReader struct {
	reader    io.Reader
	exhausted bool
}

func (r *trackedDataReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.exhausted = true
	}
	return n, err
}

type dataEvaluation struct {
	result          smtp.DataResult
	completed       bool
	closeConnection bool
	defect          error
	cause           error
}

type dataHandler func(context.Context, io.Reader) (smtp.DataResult, error)

// evaluateDataCall owns the incomplete-reader defence. It does not return
// until the framing reader reaches its real end indication, so its caller can
// neither write a final reply nor resume command parsing prematurely.
func evaluateDataCall(
	ctx context.Context,
	mode listenerMode,
	recipients []string,
	command string,
	reader io.Reader,
	handler dataHandler,
) dataEvaluation {
	tracked := &trackedDataReader{reader: reader}
	result, callErr := handler(ctx, tracked)
	returnedEarly := !tracked.exhausted
	if returnedEarly {
		if _, err := io.Copy(io.Discard, tracked); errors.Is(err, errMessageTooLarge) {
			_, err = io.Copy(io.Discard, tracked)
			if err != nil {
				return dataEvaluation{closeConnection: true, cause: fmt.Errorf("smtpserver: drain message data: %w", err)}
			}
		} else if err != nil {
			return dataEvaluation{closeConnection: true, cause: fmt.Errorf("smtpserver: drain message data: %w", err)}
		}
	}

	evaluation := validateDataOutcome(mode, recipients, command, result, callErr)
	if !returnedEarly || evaluation.closeConnection {
		return evaluation
	}
	replaced := false
	for i := range evaluation.result {
		if evaluation.result[i].Accepted() {
			evaluation.result[i].Code = 451
			evaluation.result[i].Enhanced = smtp.EnhancedCode{Class: 4, Subject: 3, Detail: 0}
			evaluation.result[i].Text = "Temporary server failure"
			replaced = true
		}
	}
	if replaced {
		evaluation.completed = false
		evaluation.defect = errors.Join(evaluation.defect, fmt.Errorf("%w: Data returned success before consuming content", errBackendDataContract))
	}
	return evaluation
}

func validateDataOutcome(
	mode listenerMode,
	recipients []string,
	command string,
	result smtp.DataResult,
	callErr error,
) dataEvaluation {
	if len(recipients) == 0 {
		return invalidDataOutcome(mode, recipients, command, callErr, "called with no accepted recipients")
	}
	if callErr != nil {
		if len(result) != 0 {
			return invalidDataOutcome(mode, recipients, command, callErr, "returned both a result and an error")
		}
		return dataEvaluation{result: temporaryDataResult(mode, recipients, command), cause: callErr}
	}
	want := 1
	if mode == modeLMTP {
		want = len(recipients)
	}
	if len(result) != want {
		return invalidDataOutcome(mode, recipients, command, nil, fmt.Sprintf("returned %d results, want %d", len(result), want))
	}
	if mode == modeLMTP {
		for i := range result {
			if result[i].Recipient != recipients[i] {
				return invalidDataOutcome(mode, recipients, command, nil, fmt.Sprintf("result %d names recipient %q, want %q", i, result[i].Recipient, recipients[i]))
			}
		}
	}
	var defect error
	for i := range result {
		var repaired bool
		result[i], repaired = normalizeRecipientResult(result[i])
		if repaired {
			defect = errors.Join(defect, fmt.Errorf("%w: result %d enhanced status class disagrees with reply code", errBackendDataContract, i))
		}
	}
	return dataEvaluation{result: result, completed: true, defect: defect}
}

func invalidDataOutcome(mode listenerMode, recipients []string, command string, cause error, detail string) dataEvaluation {
	return dataEvaluation{
		result: temporaryDataResult(mode, recipients, command),
		defect: fmt.Errorf("%w: %s", errBackendDataContract, detail),
		cause:  cause,
	}
}

func temporaryDataResult(mode listenerMode, recipients []string, command string) smtp.DataResult {
	count := 1
	if mode == modeLMTP {
		count = len(recipients)
	}
	result := make(smtp.DataResult, count)
	for i := range result {
		recipient := ""
		if mode == modeLMTP && i < len(recipients) {
			recipient = recipients[i]
		}
		result[i] = smtp.RecipientResult{
			Recipient: recipient,
			Command:   command,
			Code:      451,
			Enhanced:  smtp.EnhancedCode{Class: 4, Subject: 3, Detail: 0},
			Text:      "Temporary server failure",
		}
	}
	return result
}
