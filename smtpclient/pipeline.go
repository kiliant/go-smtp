package smtpclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// RFC 2920 calls out the risk of deadlock if each endpoint fills its TCP
// window before reading. Both limits are deliberately finite even though most
// groups are much smaller.
const (
	maxPipelineDepth = 16
	maxPipelineBytes = 64 << 10
)

type queuedCommand struct {
	verb      string
	args      []string
	syncPoint bool
	timeout   time.Duration
}

type pipeline struct {
	conn *connection
}

func isSyncPoint(verb string) bool {
	switch strings.ToUpper(verb) {
	case "EHLO", "DATA", "VRFY", "EXPN", "TURN", "QUIT", "NOOP":
		return true
	default:
		return false
	}
}

func (p *pipeline) depth() int {
	p.conn.mu.Lock()
	defer p.conn.mu.Unlock()
	if _, ok := p.conn.ext["PIPELINING"]; ok {
		return maxPipelineDepth
	}
	return 1
}

// execute runs a group through one FIFO, issuing a bounded prefix before
// reading replies in exact issue order. In the non-PIPELINING case depth is
// one, not a distinct code path.
func (p *pipeline) execute(ctx context.Context, commands []queuedCommand) ([]smtpwire.Reply, error) {
	if len(commands) == 0 {
		return nil, nil
	}
	if err := validateGroup(commands); err != nil {
		return nil, err
	}
	p.conn.opMu.Lock()
	defer p.conn.opMu.Unlock()
	return p.executeLocked(ctx, commands)
}

// executeLocked is execute for the one caller (Close) that already owns the
// operation lock. Keeping the queue logic in one place avoids a subtly
// different shutdown path.
func (p *pipeline) executeLocked(ctx context.Context, commands []queuedCommand) ([]smtpwire.Reply, error) {
	if p.conn.closed() {
		return nil, errors.New("smtpclient: connection is closed")
	}

	results := make([]smtpwire.Reply, 0, len(commands))
	for start := 0; start < len(commands); {
		limit := p.depth()
		end := start
		bytes := 0
		for end < len(commands) && end-start < limit {
			cmd := commands[end]
			encoded := commandLength(cmd)
			if encoded > maxPipelineBytes {
				return nil, fmt.Errorf("smtpclient: %s command exceeds pipeline byte bound of %d", cmd.verb, maxPipelineBytes)
			}
			if end > start && bytes+encoded > maxPipelineBytes {
				break
			}
			if err := p.write(ctx, cmd); err != nil {
				return nil, err
			}
			bytes += encoded
			end++
			if cmd.syncPoint {
				break
			}
		}
		for i := start; i < end; i++ {
			reply, err := p.read(ctx, commands[i].verb, commands[i].timeout)
			if err != nil {
				return nil, err
			}
			results = append(results, reply)
		}
		start = end
	}
	return results, nil
}

func validateGroup(commands []queuedCommand) error {
	for i := range commands {
		commands[i].verb = strings.ToUpper(commands[i].verb)
		if commands[i].syncPoint || isSyncPoint(commands[i].verb) {
			if i != len(commands)-1 {
				return fmt.Errorf("smtpclient: %s is a pipelining sync point and must be last in a group", commands[i].verb)
			}
		}
	}
	return nil
}

func commandLength(cmd queuedCommand) int {
	n := len(cmd.verb) + 2
	for _, arg := range cmd.args {
		n += len(arg) + 1
	}
	return n
}

func (p *pipeline) write(ctx context.Context, cmd queuedCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := p.conn.cancelWatcher(ctx)
	p.conn.mu.Lock()
	raw := p.conn.raw
	p.conn.mu.Unlock()
	err := smtpwire.EncodeCommand(raw, cmd.verb, cmd.args...)
	stop()
	if ctx.Err() != nil {
		p.conn.poison()
		return ctx.Err()
	}
	if err != nil {
		p.conn.poison()
		return transportError(cmd.verb, err)
	}
	return nil
}

func (p *pipeline) readOnly(ctx context.Context, command string, timeout time.Duration) (smtpwire.Reply, error) {
	if p.conn.closed() {
		return smtpwire.Reply{}, errors.New("smtpclient: connection is closed")
	}
	return p.read(ctx, command, timeout)
}

func (p *pipeline) read(ctx context.Context, command string, timeout time.Duration) (smtpwire.Reply, error) {
	if err := ctx.Err(); err != nil {
		return smtpwire.Reply{}, err
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	stop := p.conn.cancelWatcher(ctx)
	p.conn.mu.Lock()
	reader := p.conn.reader
	p.conn.mu.Unlock()
	reply, err := reader.ReadReply(deadline, smtpwire.Limits{})
	stop()
	if ctx.Err() != nil {
		p.conn.poison()
		return smtpwire.Reply{}, ctx.Err()
	}
	if err != nil {
		p.conn.poison()
		return smtpwire.Reply{}, transportError(command, err)
	}
	if reply.Code == 421 {
		p.conn.poison()
		return smtpwire.Reply{}, replyError(command, reply, p.conn.enhancedStatusCodes())
	}
	return reply, nil
}
