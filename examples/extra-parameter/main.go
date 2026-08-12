// Command extra-parameter sends an unmodelled RFC 5321 esmtp-param.
package main

import (
	"context"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpclient"
)

func main() {
	ctx := context.Background()
	c, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: "relay.example:25"})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	if _, ok := c.Extension(smtp.Extension("FUTURE-EXT")); !ok {
		panic("server does not advertise FUTURE-EXT")
	}
	opts := &smtp.MailOptions{Extra: []smtp.Param{{Keyword: "FUTURE-EXT", Value: smtp.EncodeXtext("job+42")}}}
	if err := c.Mail(ctx, "sender@example", opts, nil); err != nil {
		panic(err)
	}
}
