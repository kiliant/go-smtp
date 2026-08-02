package smtpclient

import (
	"context"
	"io"
	"sort"
	"sync"

	smtp "github.com/kiliant/go-smtp"
)

// The extension tasks own concrete encoders in ext_[abc]_*.go. This small
// registry is the sole transaction-core seam: it keeps the T05 command path
// independent of an ever-growing list of extension-specific fields.
type mailExtensionEncoder func(*Client, *smtp.MailOptions) ([]smtp.Param, error)
type rcptExtensionEncoder func(*Client, *smtp.RcptOptions) ([]smtp.Param, error)
type dataExtensionHandler func(context.Context, *Client, io.Reader, *DataOptions) (smtp.DataResult, bool, error)

type namedMailEncoder struct {
	name string
	fn   mailExtensionEncoder
}
type namedRcptEncoder struct {
	name string
	fn   rcptExtensionEncoder
}

var extensionHooks struct {
	sync.RWMutex
	mail []namedMailEncoder
	rcpt []namedRcptEncoder
	data dataExtensionHandler
}

func registerMailExtension(name string, fn mailExtensionEncoder) {
	extensionHooks.Lock()
	defer extensionHooks.Unlock()
	extensionHooks.mail = append(extensionHooks.mail, namedMailEncoder{name, fn})
	sort.Slice(extensionHooks.mail, func(i, j int) bool { return extensionHooks.mail[i].name < extensionHooks.mail[j].name })
}

func registerRcptExtension(name string, fn rcptExtensionEncoder) {
	extensionHooks.Lock()
	defer extensionHooks.Unlock()
	extensionHooks.rcpt = append(extensionHooks.rcpt, namedRcptEncoder{name, fn})
	sort.Slice(extensionHooks.rcpt, func(i, j int) bool { return extensionHooks.rcpt[i].name < extensionHooks.rcpt[j].name })
}

func registerDataExtension(fn dataExtensionHandler) {
	extensionHooks.Lock()
	defer extensionHooks.Unlock()
	extensionHooks.data = fn
}

func (c *Client) extensionMailParams(opts *smtp.MailOptions) ([]smtp.Param, error) {
	extensionHooks.RLock()
	hooks := append([]namedMailEncoder(nil), extensionHooks.mail...)
	extensionHooks.RUnlock()
	var params []smtp.Param
	for _, hook := range hooks {
		p, err := hook.fn(c, opts)
		if err != nil {
			return nil, err
		}
		params = append(params, p...)
	}
	return params, nil
}

func (c *Client) extensionRcptParams(opts *smtp.RcptOptions) ([]smtp.Param, error) {
	extensionHooks.RLock()
	hooks := append([]namedRcptEncoder(nil), extensionHooks.rcpt...)
	extensionHooks.RUnlock()
	var params []smtp.Param
	for _, hook := range hooks {
		p, err := hook.fn(c, opts)
		if err != nil {
			return nil, err
		}
		params = append(params, p...)
	}
	return params, nil
}

func extensionData(ctx context.Context, c *Client, r io.Reader, opts *DataOptions) (smtp.DataResult, bool, error) {
	extensionHooks.RLock()
	hook := extensionHooks.data
	extensionHooks.RUnlock()
	if hook == nil {
		return nil, false, nil
	}
	return hook(ctx, c, r, opts)
}
