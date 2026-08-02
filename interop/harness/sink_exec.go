package harness

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Execer is the subset of *Handle a maildir sink needs, so tests can supply
// a fake without starting a container.
type Execer interface {
	Exec(ctx context.Context, args ...string) ([]byte, error)
}

// MaildirSink reads delivered mail from a Maildir (RFC-less but
// de-facto-standard "new"/"cur" layout) inside a running container via
// "podman exec". It covers every Tier 1/2 server profile except the two with
// an HTTP retrieval API (Mailpit, GreenMail).
type MaildirSink struct {
	Exec Execer
	// Dir returns the maildir root for recipient (the directory containing
	// "new" and "cur"), since each server's mailbox layout differs.
	Dir func(recipient string) string
}

// maildirEntry is one listed file, kept alongside its full path so entries
// from "new" and "cur" can be sorted together before reading.
type maildirEntry struct {
	name, path string
}

// Fetch lists and reads every message in "new" and "cur" for recipient.
// Maildir filenames begin with a delivery timestamp, so entries from both
// subdirectories are sorted together by name before being read: callers
// such as WaitForMessage take the first result as "the" message, and
// without a deterministic order that would be arbitrary (filesystem
// listing order) whenever more than one message is present.
func (s MaildirSink) Fetch(ctx context.Context, recipient string) ([]Message, error) {
	dir := s.Dir(recipient)
	var entries []maildirEntry
	for _, sub := range []string{"new", "cur"} {
		out, err := s.Exec.Exec(ctx, "sh", "-c", fmt.Sprintf("ls -1 %s 2>/dev/null || true", shQuote(dir+"/"+sub)))
		if err != nil {
			return nil, fmt.Errorf("harness: listing %s/%s: %w", dir, sub, err)
		}
		for _, name := range splitNonEmptyLines(string(out)) {
			entries = append(entries, maildirEntry{name: name, path: dir + "/" + sub + "/" + name})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	msgs := make([]Message, 0, len(entries))
	for _, e := range entries {
		raw, err := s.Exec.Exec(ctx, "cat", e.path)
		if err != nil {
			return nil, fmt.Errorf("harness: reading %s: %w", e.path, err)
		}
		msgs = append(msgs, Message{Recipient: recipient, Raw: raw})
	}
	return msgs, nil
}

// Reset removes every message under "new" and "cur" for recipient.
func (s MaildirSink) Reset(ctx context.Context, recipient string) error {
	dir := s.Dir(recipient)
	_, err := s.Exec.Exec(ctx, "sh", "-c", fmt.Sprintf("rm -f %s/new/* %s/cur/* 2>/dev/null || true", shQuote(dir), shQuote(dir)))
	if err != nil {
		return fmt.Errorf("harness: resetting %s: %w", dir, err)
	}
	return nil
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
