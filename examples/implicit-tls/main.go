// Command implicit-tls demonstrates RFC 8314 implicit-TLS submission on port 465.
package main

import (
	"context"
	"strings"

	"github.com/kiliant/go-smtp/smtpclient"
)

func main() {
	ctx := context.Background()
	c, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: "submit.example:465", TLSServerName: "submit.example", ImplicitTLS: true})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	if err := c.Auth(ctx, &smtpclient.AuthOptions{Username: "alice", Password: "secret"}); err != nil {
		panic(err)
	}
	if err := c.Mail(ctx, "alice@example", nil); err != nil {
		panic(err)
	}
	if err := c.Rcpt(ctx, "bob@example", nil); err != nil {
		panic(err)
	}
	_, err = c.Data(ctx, strings.NewReader("Subject: hello\r\n\r\nHello.\r\n"), nil)
	if err != nil {
		panic(err)
	}
}
