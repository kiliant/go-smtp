// Command smtpclient-test-double uses the supported memory backend as a
// self-contained RFC 5321 test double for an smtpclient caller.
package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/kiliant/go-smtp/smtpclient"
	"github.com/kiliant/go-smtp/smtpserver"
	"github.com/kiliant/go-smtp/smtpserver/memory"
)

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	sink := memory.New(nil)
	server, err := smtpserver.NewServer(&smtpserver.ServerOptions{
		Listener: listener,
		Backend:  sink.Backend(),
	})
	if err != nil {
		panic(err)
	}
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serveCtx, nil) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: listener.Addr().String()})
	if err != nil {
		panic(err)
	}
	if err := client.Mail(ctx, "sender@example.test", nil, nil); err != nil {
		panic(err)
	}
	if err := client.Rcpt(ctx, "recipient@example.test", nil, nil); err != nil {
		panic(err)
	}
	if _, err := client.Data(ctx, strings.NewReader("Subject: test double\r\n\r\nhello\r\n"), nil); err != nil {
		panic(err)
	}
	if err := client.Close(); err != nil {
		panic(err)
	}

	messages := sink.Messages()
	if len(messages) != 1 {
		panic(fmt.Sprintf("got %d messages, want 1", len(messages)))
	}
	fmt.Printf("accepted for %s\n", messages[0].Recipients[0])

	if err := server.Shutdown(ctx, nil); err != nil {
		panic(err)
	}
	if err := <-serveErr; err != nil {
		panic(err)
	}
}
