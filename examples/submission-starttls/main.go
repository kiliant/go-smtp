// Command submission-starttls demonstrates RFC 6409 submission on port 587
// with STARTTLS (RFC 3207) and SMTP AUTH (RFC 4954).
package main

import (
	"context"
	"strings"

	"github.com/kiliant/go-smtp/smtpclient"
)

func main() {
	ctx := context.Background()
	c, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: "submit.example:587", TLSServerName: "submit.example"})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	if err := c.StartTLS(ctx, nil); err != nil {
		panic(err)
	}
	if err := c.Auth(ctx, &smtpclient.AuthOptions{Username: "alice", Password: "secret"}); err != nil {
		panic(err)
	}
	if err := c.Mail(ctx, "alice@example", nil, nil); err != nil {
		panic(err)
	}
	if err := c.Rcpt(ctx, "bob@example", nil, nil); err != nil {
		panic(err)
	}
	_, err = c.Data(ctx, strings.NewReader("From: alice@example\r\nTo: bob@example\r\n\r\nHello.\r\n"), nil)
	if err != nil {
		panic(err)
	}
}
