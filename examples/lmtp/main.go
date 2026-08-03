// Command lmtp demonstrates local delivery with LMTP (RFC 2033).
package main

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/kiliant/go-smtp/smtpclient"
)

func main() {
	ctx := context.Background()
	dial := func(ctx context.Context, _, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", addr)
	}
	c, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: "/run/dovecot/lmtp", LMTP: true, DialContext: dial})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	if err := c.Mail(ctx, "sender@example", nil); err != nil {
		panic(err)
	}
	if _, err := c.RcptBatch(ctx, []smtpclient.Recipient{{Address: "one@example"}, {Address: "two@example"}}, nil); err != nil {
		panic(err)
	}
	result, err := c.Data(ctx, strings.NewReader("Subject: LMTP\r\n\r\nHello.\r\n"), nil)
	if err != nil {
		panic(err)
	}
	for _, recipient := range result {
		fmt.Printf("%s: %d %s\n", recipient.Recipient, recipient.Code, recipient.Text)
	}
}
