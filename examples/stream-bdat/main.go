// Command stream-bdat streams content with CHUNKING/BDAT (RFC 3030).
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
	if err := c.Mail(ctx, "sender@example", nil, nil); err != nil {
		panic(err)
	}
	if err := c.Rcpt(ctx, "recipient@example", nil, nil); err != nil {
		panic(err)
	}
	_, err = c.Data(ctx, message, &smtpclient.DataOptions{UseChunking: true, ChunkSize: 64 << 10})
	if err != nil {
		panic(err)
	}
}
