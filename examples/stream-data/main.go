// Command stream-data streams a large RFC 5321 DATA body without buffering it.
package main

import (
	"context"
	"os"

	"github.com/kiliant/go-smtp/smtpclient"
)

func main() {
	ctx := context.Background()
	message, err := os.Open("message.eml")
	if err != nil {
		panic(err)
	}
	defer message.Close()
	c, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: "relay.example:25"})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	if err := c.Mail(ctx, "sender@example", nil); err != nil {
		panic(err)
	}
	if err := c.Rcpt(ctx, "recipient@example", nil); err != nil {
		panic(err)
	}
	_, err = c.Data(ctx, message, nil)
	if err != nil {
		panic(err)
	}
}
