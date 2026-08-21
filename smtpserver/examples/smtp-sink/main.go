// Command smtp-sink runs a minimal non-durable RFC 5321 SMTP sink.
package main

import (
	"context"
	"flag"
	"net"
	"os/signal"
	"syscall"

	"github.com/kiliant/go-smtp/smtpserver"
	"github.com/kiliant/go-smtp/smtpserver/memory"
)

func main() {
	address := flag.String("listen", "127.0.0.1:2525", "SMTP listen address")
	flag.Parse()

	listener, err := net.Listen("tcp", *address)
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := server.Serve(ctx, nil); err != nil && ctx.Err() == nil {
		panic(err)
	}
}
