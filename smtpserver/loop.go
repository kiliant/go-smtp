package smtpserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

type commandAction struct {
	synchronizationPoint bool
	closeConnection      bool
}

type commandExecutor func(context.Context, smtpwire.Command, *smtpwire.LineReader, *bufio.Writer) (commandAction, error)

type commandLoop struct {
	reader       *smtpwire.LineReader
	writer       *bufio.Writer
	limits       smtpwire.Limits
	readDeadline func() time.Time
	execute      commandExecutor
}

func (loop *commandLoop) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// A non-empty decoder buffer proves that the next command can be read
		// without blocking. Otherwise flush every pending reply first. This is
		// RFC 2920 section 3.2's progress guarantee expressed structurally.
		if loop.reader.Buffered() == 0 {
			if err := loop.writer.Flush(); err != nil {
				return fmt.Errorf("smtpserver: flush replies before read: %w", err)
			}
		}
		var deadline time.Time
		if loop.readDeadline != nil {
			deadline = loop.readDeadline()
		}
		command, err := loop.reader.ReadCommand(deadline, loop.limits)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("smtpserver: read command: %w", err)
		}
		action, err := loop.execute(ctx, command, loop.reader, loop.writer)
		if err != nil {
			return err
		}
		if action.synchronizationPoint || action.closeConnection {
			if err := loop.writer.Flush(); err != nil {
				return fmt.Errorf("smtpserver: flush command reply: %w", err)
			}
		}
		if action.closeConnection {
			return nil
		}
	}
}
