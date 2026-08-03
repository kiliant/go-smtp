// Command partial-rejection demonstrates RFC 5321 multi-recipient delivery.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/kiliant/go-smtp/smtpclient"
)

func main() {
	ctx := context.Background()
	c, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: "relay.example:25"})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	if err := c.Mail(ctx, "sender@example", nil); err != nil {
		panic(err)
	}
	rcpts, err := c.RcptBatch(ctx, []smtpclient.Recipient{{Address: "accepted@example"}, {Address: "rejected@example"}}, nil)
	if err != nil {
		panic(err)
	}
	for _, result := range rcpts {
		fmt.Printf("%s: %d %s\n", result.Recipient, result.Code, result.Text)
	}
	if len(rcpts.Errors()) == len(rcpts) {
		return
	}
	result, err := c.Data(ctx, strings.NewReader("Subject: partial delivery\r\n\r\nHello.\r\n"), nil)
	if err != nil {
		panic(err)
	}
	for _, recipient := range result {
		fmt.Printf("final %s: %d\n", recipient.Recipient, recipient.Code)
	}
}
