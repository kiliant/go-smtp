// Command dsn requests delivery-status notifications (RFC 3461).
package main

import (
	"context"
	"strings"

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
	mail := &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DSN: &smtp.DSNMailOptions{Return: smtp.DSNReturnHeaders, EnvelopeID: "job-42"}}}
	if err := c.Mail(ctx, "sender@example", mail); err != nil {
		panic(err)
	}
	rcpt := &smtp.RcptOptions{Delivery: &smtp.RecipientDeliveryOptions{DSN: &smtp.DSNRcptOptions{Notify: []smtp.DSNNotify{smtp.DSNNotifyFailure, smtp.DSNNotifyDelay}}}}
	if err := c.Rcpt(ctx, "recipient@example", rcpt); err != nil {
		panic(err)
	}
	_, err = c.Data(ctx, strings.NewReader("Subject: DSN\r\n\r\nHello.\r\n"), nil)
	if err != nil {
		panic(err)
	}
}
