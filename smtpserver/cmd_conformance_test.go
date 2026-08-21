package smtpserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiliant/go-smtp"
)

func TestMandatoryCommandsAndHELOBaseSMTP(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), nil)

	for _, test := range []struct {
		command string
		code    int
	}{
		{command: "NOOP before greeting.example", code: 250},
		{command: "MAIL FROM:<before@example.test>", code: 503},
		{command: "LHLO client.example", code: 500},
		{command: "FUTURE", code: 500},
		{command: "EHLO bad_identity", code: 501},
		{command: "HELO client.example", code: 250},
		{command: "MAIL FROM:<sender@example.test> SIZE=1", code: 501},
		{command: "VRFY user@example.test", code: 252},
		{command: "EXPN list", code: 502},
		{command: "HELP", code: 502},
		{command: "ETRN example.test", code: 502},
		{command: "MAIL FROM:<sender@example.test>", code: 250},
		{command: "RCPT TO:<recipient@example.test>", code: 250},
		{command: "RSET", code: 250},
		{command: "QUIT", code: 221},
	} {
		code, _ := harness.command(test.command)
		if code != test.code {
			t.Fatalf("%s code = %d, want %d", test.command, code, test.code)
		}
	}
}

func TestUnknownParametersReachBackendAndMalformedParameterIsNamed(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), nil)
	harness.wantCommand("EHLO client.example", 250)

	code, text := harness.command("MAIL FROM:<sender@example.test> SIZE=-1")
	if code != 501 || !strings.Contains(text, "SIZE") {
		t.Fatalf("malformed SIZE reply = %d %q", code, text)
	}
	harness.wantCommand("MAIL FROM:<sender@example.test> X-Future=Opaque", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test> X-Rcpt=Value", 250)

	mail, rcpt := backend.parameters()
	if mail == nil || len(mail.Extra) != 1 || mail.Extra[0] != (smtp.Param{Keyword: "X-Future", Value: "Opaque"}) {
		t.Fatalf("MAIL parameters = %+v", mail)
	}
	if rcpt == nil || len(rcpt.Extra) != 1 || rcpt.Extra[0] != (smtp.Param{Keyword: "X-Rcpt", Value: "Value"}) {
		t.Fatalf("RCPT parameters = %+v", rcpt)
	}
}

func TestDATAAndBDATRequireAcceptedRecipient(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	backend.rejectRecipients = true
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), enableTestChunking)
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<rejected@example.test>", 550)
	harness.wantCommand("DATA", 503)
	writeTestCommand(t, harness.conn, "BDAT 3 LAST\r\nabcNOOP\r\n")
	if code, _ := readTestReplyDetails(t, harness.reader); code != 503 {
		t.Fatalf("zero-recipient BDAT reply = %d, want 503", code)
	}
	if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
		t.Fatalf("pipelined NOOP reply = %d, want 250", code)
	}
	if calls := backend.dataCallCount(); calls != 0 {
		t.Fatalf("Session.Data calls = %d, want 0", calls)
	}
}

func TestRequireAuthRejectsSessionWithoutCompatibleVerifier(t *testing.T) {
	listener, clientConn := newPipeListener()
	var logged error
	server, err := NewServer(&ServerOptions{
		Listener:                listener,
		Backend:                 newCommandTestBackend(ModeSMTP).backend(),
		GreetingIdentity:        "server.example",
		RequireAuth:             true,
		AuthMechanismsBeforeTLS: []string{"PLAIN"},
		ErrorLog: func(event ErrorEvent) {
			logged = event.Err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, nil) }()
	reader := bufio.NewReader(clientConn)
	if code, _ := readTestReplyDetails(t, reader); code != 421 {
		t.Fatalf("greeting code = %d, want 421", code)
	}
	_ = clientConn.Close()
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !errors.Is(logged, errBackendAuthContract) {
		t.Fatalf("logged error = %v, want backend auth contract", logged)
	}
}

func TestBDATConsumesExactlyChunkBeforeNextCommand(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), enableTestChunking)
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)

	writeTestCommand(t, harness.conn, "BDAT 3\r\nabcNOOP\r\n")
	if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
		t.Fatalf("BDAT reply = %d, want 250", code)
	}
	if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
		t.Fatalf("pipelined NOOP reply = %d, want 250", code)
	}
}

func TestOversizeBDATConsumesAnnouncedChunkBeforeFailure(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		enableTestChunking(opts)
		opts.MaxMessageBytes = 3
		opts.MaxSpoolBytes = 3
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)

	writeTestCommand(t, harness.conn, "BDAT 4\r\nabcdRSET\r\n")
	if code, _ := readTestReplyDetails(t, harness.reader); code != 552 {
		t.Fatalf("oversize BDAT reply = %d, want 552", code)
	}
	if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
		t.Fatalf("pipelined RSET reply = %d, want 250", code)
	}
	if calls := backend.dataCallCount(); calls != 0 {
		t.Fatalf("Session.Data calls = %d, want 0", calls)
	}
}

func TestReplacementMAILResetsBeforeSizeRejection(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxMessageBytes = 10
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<first@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
	harness.wantCommand("MAIL FROM:<second@example.test> SIZE=11", 552)
	if reasons := backend.resetReasonSnapshot(); len(reasons) != 1 || reasons[0] != ResetNewMail {
		t.Fatalf("Reset reasons = %#v, want ResetNewMail", reasons)
	}
	harness.wantCommand("RCPT TO:<orphan@example.test>", 503)
}

func TestBDATShortReadDoesNotCompleteChunk(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		enableTestChunking(opts)
		opts.DataTimeout = 100 * time.Millisecond
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)

	writeTestCommand(t, harness.conn, "BDAT 3\r\nab")
	if err := harness.conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err := harness.reader.ReadString('\n')
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("read before complete chunk = %v, want timeout", err)
	}
	if calls := backend.dataCallCount(); calls != 0 {
		t.Fatalf("Session.Data calls = %d, want 0", calls)
	}
}

func TestSizeLimitCountsClientOctetsNotReceivedHeader(t *testing.T) {
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxMessageBytes = 4
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
	harness.wantCommand("DATA", 354)
	writeTestCommand(t, harness.conn, "12\r\n.\r\n")
	if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
		t.Fatalf("exact-limit DATA reply = %d, want 250", code)
	}
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
	harness.wantCommand("DATA", 354)
	writeTestCommand(t, harness.conn, "123\r\n.\r\n")
	if code, _ := readTestReplyDetails(t, harness.reader); code != 552 {
		t.Fatalf("oversize DATA reply = %d, want 552", code)
	}
	if got := backend.message(); !strings.HasPrefix(got, "Received:") {
		t.Fatalf("backend message lacks Received header: %q", got)
	}
}

func TestAUTHOversizeContinuationGetsReplyAfterFullLine(t *testing.T) {
	recorder := &recordingBackend{mode: ModeSMTP, authenticate: true}
	harness := newRawTestServer(t, ModeSMTP, recorder.backend(), func(opts *ServerOptions) {
		opts.AuthMechanismsBeforeTLS = []string{"PLAIN"}
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("AUTH PLAIN", 334)
	writeTestCommand(t, harness.conn, strings.Repeat("A", 33<<10)+"\r\n")
	code, text := readTestReplyDetails(t, harness.reader)
	if code != 500 || !strings.Contains(text, "5.5.6") {
		t.Fatalf("oversize AUTH reply = %d %q", code, text)
	}
}

func enableTestChunking(opts *ServerOptions) {
	opts.EnableCHUNKING = true
	opts.MaxSpoolBytes = 1 << 20
	opts.MaxSpoolMemoryBytes = 64
	opts.MaxTotalSpoolBytes = 2 << 20
	opts.MaxTotalSpoolMemoryBytes = 128
	opts.MaxConcurrentSpools = 2
}

type rawTestServer struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

func newRawTestServer(t *testing.T, mode Mode, backend *Backend, configure func(*ServerOptions)) *rawTestServer {
	t.Helper()
	listener, clientConn := newPipeListener()
	opts := &ServerOptions{
		Listener:         listener,
		Backend:          backend,
		Mode:             mode,
		GreetingIdentity: "server.example",
		MaxMessageBytes:  1 << 20,
		SpoolDir:         t.TempDir(),
	}
	if configure != nil {
		configure(opts)
	}
	server, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, nil) }()
	harness := &rawTestServer{t: t, conn: clientConn, reader: bufio.NewReader(clientConn), cancel: cancel, done: done}
	if code, _ := readTestReplyDetails(t, harness.reader); code != 220 {
		t.Fatalf("greeting code = %d, want 220", code)
	}
	t.Cleanup(harness.close)
	return harness
}

func (h *rawTestServer) command(command string) (int, string) {
	h.t.Helper()
	writeTestCommand(h.t, h.conn, command+"\r\n")
	return readTestReplyDetails(h.t, h.reader)
}

func (h *rawTestServer) wantCommand(command string, want int) {
	h.t.Helper()
	code, _ := h.command(command)
	if code != want {
		h.t.Fatalf("%s code = %d, want %d", command, code, want)
	}
}

func (h *rawTestServer) readUntilClose() error {
	h.t.Helper()
	if err := h.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		// net.Pipe reports an already-observed peer close from SetReadDeadline
		// rather than the following read on some Go/OS combinations. That is the
		// successful condition these tests are waiting for, not a harness error.
		if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
			return err
		}
		h.t.Fatal(err)
	}
	_, err := h.reader.ReadString('\n')
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		h.t.Fatalf("server did not close the connection before the test deadline: %v", err)
	}
	return err
}

func (h *rawTestServer) close() {
	h.once.Do(func() {
		_ = h.conn.Close()
		h.cancel()
		select {
		case err := <-h.done:
			if err != nil && !errors.Is(err, context.Canceled) {
				h.t.Errorf("Serve: %v", err)
			}
		case <-time.After(time.Second):
			h.t.Error("Serve did not stop")
		}
	})
}

func readTestReplyDetails(t *testing.T, reader *bufio.Reader) (int, string) {
	t.Helper()
	var text []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if len(line) < 4 {
			t.Fatalf("short reply %q", line)
		}
		code := int(line[0]-'0')*100 + int(line[1]-'0')*10 + int(line[2]-'0')
		text = append(text, strings.TrimSuffix(strings.TrimSuffix(line[4:], "\n"), "\r"))
		if line[3] == ' ' {
			return code, strings.Join(text, "\n")
		}
	}
}

type commandTestBackend struct {
	mu               sync.Mutex
	mode             Mode
	rejectRecipients bool
	mailParams       *smtp.MailOptions
	rcptParams       *smtp.RcptOptions
	dataCalls        int
	data             string
	resetReasons     []ResetReason
}

func newCommandTestBackend(mode Mode) *commandTestBackend {
	return &commandTestBackend{mode: mode}
}

func (b *commandTestBackend) backend() *Backend {
	return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
		var recipients []string
		return &Session{
			Mail: func(_ context.Context, _ string, params *smtp.MailOptions, _ *MailOptions) error {
				b.mu.Lock()
				b.mailParams = params
				b.mu.Unlock()
				recipients = nil
				return nil
			},
			Rcpt: func(_ context.Context, recipient string, params *smtp.RcptOptions, _ *RcptOptions) error {
				if b.rejectRecipients {
					return &smtp.Error{Code: 550, Enhanced: smtp.EnhancedCode{Class: 5, Subject: 1, Detail: 1}, Text: "No such user"}
				}
				b.mu.Lock()
				b.rcptParams = params
				b.mu.Unlock()
				recipients = append(recipients, recipient)
				return nil
			},
			Data: func(_ context.Context, reader io.Reader, _ *DataOptions) (smtp.DataResult, error) {
				data, err := io.ReadAll(reader)
				b.mu.Lock()
				b.dataCalls++
				b.data = string(data)
				b.mu.Unlock()
				if err != nil {
					return nil, err
				}
				count := 1
				if b.mode == ModeLMTP {
					count = len(recipients)
				}
				result := make(smtp.DataResult, count)
				for i := range result {
					result[i] = smtp.RecipientResult{Command: "DATA", Code: 250, Enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, Text: "OK"}
					if b.mode == ModeLMTP {
						result[i].Recipient = recipients[i]
					}
				}
				return result, nil
			},
			Reset: func(_ context.Context, reason ResetReason, _ *ResetOptions) {
				b.mu.Lock()
				b.resetReasons = append(b.resetReasons, reason)
				b.mu.Unlock()
				recipients = nil
			},
			Close: func(context.Context, *CloseOptions) {},
		}, nil
	}}
}

func (b *commandTestBackend) parameters() (*smtp.MailOptions, *smtp.RcptOptions) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mailParams, b.rcptParams
}

func (b *commandTestBackend) dataCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dataCalls
}

func (b *commandTestBackend) message() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data
}

func (b *commandTestBackend) resetReasonSnapshot() []ResetReason {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ResetReason(nil), b.resetReasons...)
}
