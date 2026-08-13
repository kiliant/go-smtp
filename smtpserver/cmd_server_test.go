package smtpserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpclient"
)

func TestServerServesSMTPAndLMTPDataAndBDAT(t *testing.T) {
	for _, mode := range []Mode{ModeSMTP, ModeLMTP} {
		for _, chunking := range []bool{false, true} {
			if mode == ModeLMTP && chunking {
				// The released client's LMTP+BDAT path currently reads only one
				// final reply. Raw-wire server coverage for the required N replies
				// is separate; do not weaken the server contract to fit that client
				// follow-up.
				continue
			}
			name := string(mode) + "/DATA"
			if chunking {
				name = string(mode) + "/BDAT"
			}
			t.Run(name, func(t *testing.T) {
				recorder := &recordingBackend{mode: mode}
				listener, clientConn := newPipeListener()
				opts := &ServerOptions{
					Listener:         listener,
					Backend:          recorder.backend(),
					Mode:             mode,
					GreetingIdentity: "server.example",
					MaxMessageBytes:  1 << 20,
				}
				if chunking {
					opts.EnableCHUNKING = true
					opts.MaxSpoolBytes = 1 << 20
					opts.MaxSpoolMemoryBytes = 64
					opts.MaxTotalSpoolBytes = 2 << 20
					opts.MaxTotalSpoolMemoryBytes = 128
					opts.MaxConcurrentSpools = 2
					opts.SpoolDir = t.TempDir()
				}
				server, err := NewServer(opts)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				serveDone := make(chan error, 1)
				go func() { serveDone <- server.Serve(ctx, nil) }()

				client, err := smtpclient.NewClient(context.Background(), clientConn, &smtpclient.ClientOptions{Identity: "client.example", LMTP: mode == ModeLMTP})
				if err != nil {
					t.Fatal(err)
				}
				for _, extension := range []smtp.Extension{smtp.ExtPipelining, smtp.ExtSize, smtp.Ext8BitMIME, smtp.ExtEnhancedStatusCodes, smtp.ExtSMTPUTF8} {
					if _, ok := client.Extension(extension); !ok {
						t.Errorf("missing extension %s", extension)
					}
				}
				if _, ok := client.Extension(smtp.ExtChunking); ok != chunking {
					t.Fatalf("CHUNKING advertised = %v, want %v", ok, chunking)
				}
				if err := client.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if err := client.Rcpt(context.Background(), "same@example.test", nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				result, err := client.Data(context.Background(), strings.NewReader("Subject: test\r\n\r\nbody\r\n"), &smtpclient.DataOptions{UseChunking: chunking, ChunkSize: 7})
				if err != nil {
					t.Fatal(err)
				}
				if len(result) != 2 || !result.AllAccepted() {
					t.Fatalf("result = %+v", result)
				}
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
				cancel()
				if err := <-serveDone; err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				message := recorder.message()
				if !strings.HasPrefix(message, "Received: from client.example by server.example with ") || !strings.Contains(message, "\r\nSubject: test\r\n\r\nbody\r\n") {
					t.Fatalf("message = %q", message)
				}
				if got := recorder.recipientSnapshot(); len(got) != 2 || got[0] != "same@example.test" || got[1] != got[0] {
					t.Fatalf("recipients = %#v", got)
				}
			})
		}
	}
}

func TestServerSubmissionPolicyAndPlainAuthentication(t *testing.T) {
	recorder := &recordingBackend{mode: ModeSMTP, authenticate: true}
	listener, clientConn := newPipeListener()
	server, err := NewServer(&ServerOptions{
		Listener:                listener,
		Backend:                 recorder.backend(),
		GreetingIdentity:        "submission.example",
		RequireAuth:             true,
		AuthMechanismsBeforeTLS: []string{"PLAIN"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx, nil) }()
	client, err := smtpclient.NewClient(context.Background(), clientConn, &smtpclient.ClientOptions{Identity: "client.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Mail(context.Background(), "sender@example.test", nil, nil); err == nil {
		t.Fatal("MAIL succeeded before required AUTH")
	}
	if err := client.Auth(context.Background(), &smtpclient.AuthOptions{Username: "user", Password: "secret", Mechanisms: []string{"PLAIN"}, AllowInsecureAuth: true}); err != nil {
		t.Fatal(err)
	}
	if err := client.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	cancel()
	if err := <-serveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if recorder.authCommits != 1 {
		t.Fatalf("CommitAuth calls = %d", recorder.authCommits)
	}
}

func TestServerSTARTTLSSatisfiesTLSSubmissionPolicy(t *testing.T) {
	recorder := &recordingBackend{mode: ModeSMTP}
	listener, clientConn := newPipeListener()
	serverTLS, clientTLS := testTLSConfigs(t)
	server, err := NewServer(&ServerOptions{
		Listener:         listener,
		Backend:          recorder.backend(),
		GreetingIdentity: "server.example",
		TLSConfig:        serverTLS,
		RequireTLS:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx, nil) }()
	client, err := smtpclient.NewClient(context.Background(), clientConn, &smtpclient.ClientOptions{Identity: "client.example", TLSConfig: clientTLS})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Extension(smtp.ExtStartTLS); !ok {
		t.Fatal("STARTTLS was not advertised on the plaintext session")
	}
	if err := client.Mail(context.Background(), "sender@example.test", nil, nil); err == nil {
		t.Fatal("MAIL succeeded before required TLS")
	}
	if err := client.StartTLS(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Extension(smtp.ExtStartTLS); ok {
		t.Fatal("STARTTLS remained advertised after the TLS upgrade")
	}
	if err := client.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	cancel()
	if err := <-serveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRequireAuthWithUnavailableExternalClosesAfterSTARTTLS(t *testing.T) {
	recorder := &recordingBackend{mode: ModeSMTP, authenticate: true}
	listener, clientConn := newPipeListener()
	serverTLS, clientTLS := testTLSConfigs(t)
	server, err := NewServer(&ServerOptions{
		Listener:               listener,
		Backend:                recorder.backend(),
		GreetingIdentity:       "server.example",
		TLSConfig:              serverTLS,
		RequireAuth:            true,
		AuthMechanismsAfterTLS: []string{"EXTERNAL"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, nil) }()
	client, err := smtpclient.NewClient(context.Background(), clientConn, &smtpclient.ClientOptions{Identity: "client.example", TLSConfig: clientTLS})
	if err != nil {
		t.Fatal(err)
	}
	err = client.StartTLS(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "421") {
		t.Fatalf("StartTLS = %v, want post-handshake 421", err)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLMTPBDATLastEmitsOneReplyPerAcceptedRecipient(t *testing.T) {
	recorder := &recordingBackend{mode: ModeLMTP}
	listener, clientConn := newPipeListener()
	server, err := NewServer(&ServerOptions{
		Listener:                 listener,
		Backend:                  recorder.backend(),
		Mode:                     ModeLMTP,
		GreetingIdentity:         "lmtp.example",
		EnableCHUNKING:           true,
		MaxSpoolBytes:            1 << 20,
		MaxSpoolMemoryBytes:      64,
		MaxTotalSpoolBytes:       2 << 20,
		MaxTotalSpoolMemoryBytes: 128,
		MaxConcurrentSpools:      2,
		SpoolDir:                 t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx, nil) }()
	reader := bufio.NewReader(clientConn)
	if code := readTestReply(t, reader); code != 220 {
		t.Fatalf("greeting code = %d", code)
	}
	writeTestCommand(t, clientConn, "LHLO client.example\r\n")
	if code := readTestReply(t, reader); code != 250 {
		t.Fatalf("LHLO code = %d", code)
	}
	for _, command := range []string{
		"MAIL FROM:<sender@example.test>\r\n",
		"RCPT TO:<same@example.test>\r\n",
		"RCPT TO:<same@example.test>\r\n",
	} {
		writeTestCommand(t, clientConn, command)
		if code := readTestReply(t, reader); code != 250 {
			t.Fatalf("%s code = %d", strings.TrimSpace(command), code)
		}
	}
	writeTestCommand(t, clientConn, "BDAT 0 LAST\r\n")
	if first, second := readTestReply(t, reader), readTestReply(t, reader); first != 250 || second != 250 {
		t.Fatalf("LMTP BDAT replies = %d, %d", first, second)
	}
	writeTestCommand(t, clientConn, "QUIT\r\n")
	if code := readTestReply(t, reader); code != 221 {
		t.Fatalf("QUIT code = %d", code)
	}
	_ = clientConn.Close()
	cancel()
	if err := <-serveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func readTestReply(t *testing.T, reader *bufio.Reader) int {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if len(line) < 4 {
			t.Fatalf("short reply %q", line)
		}
		code, err := strconv.Atoi(line[:3])
		if err != nil {
			t.Fatal(err)
		}
		if line[3] == ' ' {
			return code
		}
	}
}

func writeTestCommand(t *testing.T, writer io.Writer, command string) {
	t.Helper()
	if _, err := io.WriteString(writer, command); err != nil {
		t.Fatal(err)
	}
}

type recordingBackend struct {
	mu           sync.Mutex
	mode         Mode
	reverse      string
	recipients   []string
	data         string
	authenticate bool
	authCommits  int
}

func (r *recordingBackend) backend() *Backend {
	return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
		session := &Session{
			Mail: func(_ context.Context, reverse string, _ *smtp.MailOptions, _ *MailOptions) error {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.reverse = reverse
				r.recipients = nil
				return nil
			},
			Rcpt: func(_ context.Context, recipient string, _ *smtp.RcptOptions, _ *RcptOptions) error {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.recipients = append(r.recipients, recipient)
				return nil
			},
			Data: func(_ context.Context, reader io.Reader, _ *DataOptions) (smtp.DataResult, error) {
				data, err := io.ReadAll(reader)
				if err != nil {
					return nil, err
				}
				r.mu.Lock()
				r.data = string(data)
				recipients := append([]string(nil), r.recipients...)
				r.mu.Unlock()
				count := 1
				if r.mode == ModeLMTP {
					count = len(recipients)
				}
				result := make(smtp.DataResult, count)
				for i := range result {
					result[i] = smtp.RecipientResult{Command: "DATA", Code: 250, Enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0}, Text: "OK"}
					if r.mode == ModeLMTP {
						result[i].Recipient = recipients[i]
					}
				}
				return result, nil
			},
			Reset: func(context.Context, ResetReason, *ResetOptions) {},
			Close: func(context.Context, *CloseOptions) {},
		}
		if r.authenticate {
			session.Authenticate = func(_ context.Context, credentials *Credentials, _ *AuthenticateOptions) (*AuthResult, error) {
				if credentials.AuthenticationID != "user" || credentials.Password != "secret" {
					return &AuthResult{Failure: &AuthFailure{}}, nil
				}
				return &AuthResult{Identity: "user"}, nil
			}
			session.CommitAuth = func(context.Context, *AuthResult, *CommitAuthOptions) {
				r.mu.Lock()
				r.authCommits++
				r.mu.Unlock()
			}
		}
		return session, nil
	}}
}

func (r *recordingBackend) message() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data
}

func (r *recordingBackend) recipientSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.recipients...)
}

type pipeListener struct {
	conn      net.Conn
	closed    chan struct{}
	closeOnce sync.Once
	accepted  bool
	mu        sync.Mutex
}

func newPipeListener() (*pipeListener, net.Conn) {
	client, server := net.Pipe()
	return &pipeListener{conn: server, closed: make(chan struct{})}, client
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.accepted {
		l.accepted = true
		conn := l.conn
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return testAddr("smtp") }

var _ net.Listener = (*pipeListener)(nil)
