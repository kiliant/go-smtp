package memory

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kiliant/go-smtp/internal/smtpwire"
	"github.com/kiliant/go-smtp/smtpclient"
	"github.com/kiliant/go-smtp/smtpserver"
)

func TestSinkSnapshotIsDeepCopy(t *testing.T) {
	sink := New(nil)
	sink.messages = append(sink.messages, Message{Recipients: []string{"one@example.test"}, Data: []byte("message")})
	first := sink.Messages()
	first[0].Recipients[0] = "changed"
	first[0].Data[0] = 'X'
	second := sink.Messages()
	if second[0].Recipients[0] != "one@example.test" || string(second[0].Data) != "message" {
		t.Fatalf("snapshot mutation reached sink: %+v", second[0])
	}
}

func TestSinkServesSMTPAndLMTPTransactionsFromSMTPClient(t *testing.T) {
	for _, mode := range []smtpserver.Mode{smtpserver.ModeSMTP, smtpserver.ModeLMTP} {
		t.Run(string(mode), func(t *testing.T) {
			sink := New(nil)
			clientConn, serverConn := net.Pipe()
			serverDone := make(chan error, 1)
			go func() { serverDone <- driveTestTransaction(serverConn, sink.Backend(), mode) }()

			client, err := smtpclient.NewClient(context.Background(), clientConn, &smtpclient.ClientOptions{
				Identity: "client.example",
				LMTP:     mode == smtpserver.ModeLMTP,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := client.Rcpt(context.Background(), "same@example.test", nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			result, err := client.Data(context.Background(), strings.NewReader("Subject: test\r\n\r\nbody\r\n"), nil)
			if err != nil {
				t.Fatal(err)
			}
			// smtpclient expands SMTP's one transaction reply across its two
			// accepted recipients; LMTP receives the same cardinality directly.
			wantResults := 2
			if len(result) != wantResults || !result.AllAccepted() {
				t.Fatalf("result = %+v, want %d accepted", result, wantResults)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}

			messages := sink.Messages()
			if len(messages) != 1 || messages[0].Mode != mode || messages[0].ReversePath != "sender@example.test" {
				t.Fatalf("messages = %+v", messages)
			}
			if got := messages[0].Recipients; len(got) != 2 || got[0] != "same@example.test" || got[1] != got[0] {
				t.Fatalf("recipients = %#v", got)
			}
			if string(messages[0].Data) != "Subject: test\r\n\r\nbody\r\n" {
				t.Fatalf("data = %q", messages[0].Data)
			}
		})
	}
}

func TestSessionReleasesTransactionStateForEveryResetReason(t *testing.T) {
	sink := New(nil)
	for reason := smtpserver.ResetExplicit; reason <= smtpserver.ResetSessionEnd; reason++ {
		session, err := sink.Backend().NewSession(context.Background(), &smtpserver.ConnInfo{Mode: smtpserver.ModeSMTP}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := session.Rcpt(context.Background(), "recipient@example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
		session.Reset(context.Background(), reason, nil)
		if err := session.Rcpt(context.Background(), "after-reset@example.test", nil, nil); err == nil {
			t.Fatalf("ResetReason(%d) retained transaction state", reason)
		}
		session.Close(context.Background(), nil)
		session.Close(context.Background(), nil)
	}
}

// driveTestTransaction is deliberately test-only. T20 owns production command
// dispatch; this small driver proves T19's memory backend contract against the
// released smtpclient without implementing T20 inside package smtpserver.
func driveTestTransaction(conn net.Conn, backend *smtpserver.Backend, mode smtpserver.Mode) error {
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := backend.NewSession(ctx, &smtpserver.ConnInfo{
		Mode:       mode,
		LocalAddr:  conn.LocalAddr(),
		RemoteAddr: conn.RemoteAddr(),
		TLSState:   func() *tls.ConnectionState { return nil },
	}, nil)
	if err != nil {
		return err
	}
	defer session.Close(ctx, nil)
	reader := smtpwire.NewLineReader(conn)
	writer := bufio.NewWriter(conn)
	if _, err := writer.WriteString("220 memory.test ready\r\n"); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	var recipients []string
	for {
		command, err := reader.ReadCommand(time.Now().Add(time.Second), smtpwire.Limits{})
		if err != nil {
			return err
		}
		switch strings.ToUpper(command.Verb) {
		case "EHLO":
			if mode != smtpserver.ModeSMTP {
				return fmt.Errorf("memory test driver: EHLO in LMTP mode")
			}
			_, err = writer.WriteString("250 memory.test\r\n")
		case "LHLO":
			if mode != smtpserver.ModeLMTP {
				return fmt.Errorf("memory test driver: LHLO in SMTP mode")
			}
			_, err = writer.WriteString("250 memory.test\r\n")
		case "MAIL":
			path := testPath(command.Argument, "FROM:")
			err = session.Mail(ctx, path, nil, nil)
			if err == nil {
				_, err = writer.WriteString("250 OK\r\n")
			}
		case "RCPT":
			path := testPath(command.Argument, "TO:")
			err = session.Rcpt(ctx, path, nil, nil)
			if err == nil {
				recipients = append(recipients, path)
				_, err = writer.WriteString("250 OK\r\n")
			}
		case "DATA":
			if _, err = writer.WriteString("354 continue\r\n"); err != nil {
				return err
			}
			if err = writer.Flush(); err != nil {
				return err
			}
			result, dataErr := session.Data(ctx, smtpwire.NewDotUnstuffReader(reader), nil)
			if dataErr != nil {
				return dataErr
			}
			for i, item := range result {
				if mode == smtpserver.ModeLMTP && item.Recipient != recipients[i] {
					return fmt.Errorf("memory test driver: recipient order mismatch")
				}
				if _, err = fmt.Fprintf(writer, "%d %s\r\n", item.Code, item.Text); err != nil {
					return err
				}
			}
			session.Reset(ctx, smtpserver.ResetCompleted, nil)
		case "QUIT":
			_, err = writer.WriteString("221 bye\r\n")
			if err == nil {
				err = writer.Flush()
			}
			return err
		default:
			return fmt.Errorf("memory test driver: unexpected command %q", command.Verb)
		}
		if err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func testPath(argument, prefix string) string {
	value := strings.TrimPrefix(argument, prefix)
	return strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">")
}
