package smtpclient

import (
	"context"
	"errors"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

func TestTransactionDataReturnsPerRecipientResults(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "RCPT TO:<two@example.test>", replies: fakeReplies("251 two forwarded\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 queued\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "two@example.test", nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.Data(context.Background(), strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got.AllAccepted() {
		t.Fatalf("Data result = %#v", got)
	}
	for _, result := range got {
		if result.Command != "DATA" || result.Code != 250 || result.Text != "queued" {
			t.Errorf("recipient result = %#v", result)
		}
	}
}

func TestRcptBatchUsesAdvertisedPipelining(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-PIPELINING\r\n", "250 ENHANCEDSTATUSCODES\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 2.1.0 sender ok\r\n")},
		{
			commands: []string{"RCPT TO:<one@example.test>", "RCPT TO:<two@example.test>"},
			replies:  fakeReplies("250 2.1.5 one ok\r\n", "550 5.1.1 no such user\r\n"),
		},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 2.0.0 queued\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	result, err := c.RcptBatch(context.Background(), []Recipient{
		{Address: "one@example.test"},
		{Address: "two@example.test"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Code != 250 || result[1].Code != 550 {
		t.Fatalf("RcptBatch result = %#v", result)
	}
	if result.AllAccepted() || len(result.Errors()) != 1 || result.Errors()[0].Code != 550 {
		t.Fatalf("RcptBatch acceptance helpers disagree with %#v", result)
	}
	dataResult, err := c.Data(context.Background(), strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dataResult) != 1 || dataResult[0].Recipient != "one@example.test" {
		t.Fatalf("Data recipients after RcptBatch = %#v", dataResult)
	}
}

func TestRcptBatchUsesSameDepthOneQueueWithoutAdvertisement(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "RCPT TO:<two@example.test>", replies: fakeReplies("251 two forwarded\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	result, err := c.RcptBatch(context.Background(), []Recipient{
		{Address: "one@example.test"},
		{Address: "two@example.test"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AllAccepted() {
		t.Fatalf("RcptBatch result = %#v, want all accepted", result)
	}
}

func TestDataRejectionLeavesTransactionRecoverable(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "DATA", replies: fakeReplies("554 no content\r\n")},
		{command: "RSET", replies: fakeReplies("250 reset\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(context.Background(), strings.NewReader("body"), nil); err == nil {
		t.Fatal("DATA rejection returned nil")
	}
	if err := c.Reset(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestDataRequires250FinalReply(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("251 non-standard completion\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(context.Background(), strings.NewReader(""), nil); err == nil {
		t.Fatal("DATA 251 completion returned nil")
	} else {
		var smtpErr *smtp.Error
		if !errors.As(err, &smtpErr) || smtpErr.Code != 251 {
			t.Fatalf("DATA error = %v, want *smtp.Error code 251", err)
		}
	}
	c.conn.mu.Lock()
	defer c.conn.mu.Unlock()
	if c.conn.state == stateTransaction {
		t.Fatal("DATA 251 left transaction open")
	}
}

func TestMailExtraValidatesAdvertisedExtensionBeforeWrite(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Extra: []smtp.Param{{Keyword: "SIZE", Value: "1"}}})
	if err == nil || !strings.Contains(err.Error(), "SIZE") {
		t.Fatalf("Mail error = %v, want missing SIZE extension", err)
	}
}

func TestMailExtraUsesAdvertisingExtension(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DSN\r\n")},
		{command: "MAIL FROM:<sender@example.test> RET=FULL", replies: fakeReplies("250 sender ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Extra: []smtp.Param{{Keyword: "RET", Value: "FULL"}}}); err != nil {
		t.Fatal(err)
	}
}

// TestRcptUTF8PathRequiresTransactionSMTPUTF8 covers the audit finding that
// smtpUTF8 was written but never read: a non-ASCII RCPT forward-path must be
// rejected locally, before anything reaches the wire, unless the active
// transaction's MAIL FROM requested SMTPUTF8. The fake script has no RCPT
// step, so a regression that writes the command anyway fails the fake
// server's command match rather than being silently accepted.
func TestRcptUTF8PathRequiresTransactionSMTPUTF8(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-8BITMIME\r\n", "250 SMTPUTF8\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt(context.Background(), "récipient@example.test", nil)
	if err == nil || !strings.Contains(err.Error(), "SMTPUTF8") {
		t.Fatalf("Rcpt error = %v, want local SMTPUTF8 coupling error", err)
	}
}

// TestRcptUTF8PathAllowedWhenTransactionRequestsSMTPUTF8 is the positive
// counterpart: once MAIL FROM requested SMTPUTF8 on a server that advertised
// it, a UTF-8 RCPT forward-path is sent verbatim.
func TestRcptUTF8PathAllowedWhenTransactionRequestsSMTPUTF8(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-8BITMIME\r\n", "250 SMTPUTF8\r\n")},
		{command: "MAIL FROM:<sender@example.test> SMTPUTF8", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<récipient@example.test>", replies: fakeReplies("250 recipient ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Transport: &smtp.TransportOptions{SMTPUTF8: true}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "récipient@example.test", nil); err != nil {
		t.Fatal(err)
	}
}

// TestRcptUTF8CouplingResetByRSET ensures the SMTPUTF8 coupling flag does not
// outlive the transaction it was requested for: after RSET, the next
// transaction must request SMTPUTF8 again before a UTF-8 RCPT path is
// accepted locally.
func TestRcptUTF8CouplingResetByRSET(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-8BITMIME\r\n", "250 SMTPUTF8\r\n")},
		{command: "MAIL FROM:<sender@example.test> SMTPUTF8", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RSET", replies: fakeReplies("250 reset ok\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Transport: &smtp.TransportOptions{SMTPUTF8: true}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Reset(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt(context.Background(), "récipient@example.test", nil)
	if err == nil || !strings.Contains(err.Error(), "SMTPUTF8") {
		t.Fatalf("Rcpt error after RSET = %v, want local SMTPUTF8 coupling error", err)
	}
}

// TestResetAllowedOutsideTransaction covers the audit finding that RSET was
// gated on stateTransaction only, more restrictive than RFC 5321 §4.1.1.5
// (and than Noop, which places no such restriction). It also guards against
// the naive fix of simply widening the allowed states: transactionBase is
// only meaningful once a transaction is open, so RSET issued beforehand must
// not downgrade the session state to transactionBase's zero value.
func TestResetAllowedOutsideTransaction(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "RSET", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Reset(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	c.conn.mu.Lock()
	state := c.conn.state
	c.conn.mu.Unlock()
	if state != stateGreeted {
		t.Fatalf("state after RSET outside a transaction = %v, want %v", state, stateGreeted)
	}
}

func TestParameterExtensionMappings(t *testing.T) {
	tests := []struct {
		param smtp.Param
		want  string
	}{
		{smtp.Param{Keyword: "RET", Value: "FULL"}, "DSN"},
		{smtp.Param{Keyword: "ENVID", Value: "id"}, "DSN"},
		{smtp.Param{Keyword: "BY", Value: "10"}, "DELIVERBY"},
		{smtp.Param{Keyword: "BODY", Value: "8BITMIME"}, "8BITMIME"},
		{smtp.Param{Keyword: "BODY", Value: "7BIT"}, ""},
	}
	for _, test := range tests {
		if got := parameterExtension(test.param); got != test.want {
			t.Errorf("parameterExtension(%+v) = %q, want %q", test.param, got, test.want)
		}
	}
}
