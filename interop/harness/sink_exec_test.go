package harness

import (
	"context"
	"strings"
	"testing"
)

// fakeExecer models a maildir inside a container without podman: it
// dispatches on the shape of the command the sink actually issues.
type fakeExecer struct {
	files map[string][]byte   // path -> content
	dirs  map[string][]string // dir -> file names
}

func (f *fakeExecer) Exec(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "sh" && args[1] == "-c" {
		script := args[2]
		switch {
		case strings.HasPrefix(script, "ls -1 "):
			dir := extractQuoted(script)
			names := f.dirs[dir]
			return []byte(strings.Join(names, "\n")), nil
		case strings.HasPrefix(script, "rm -f "):
			for dir := range f.dirs {
				delete(f.dirs, dir)
			}
			return nil, nil
		}
	}
	if len(args) >= 2 && args[0] == "cat" {
		return f.files[args[1]], nil
	}
	return nil, nil
}

// extractQuoted pulls the single-quoted argument out of a shell script the
// sink built with shQuote, good enough for this fake.
func extractQuoted(script string) string {
	start := strings.IndexByte(script, '\'')
	end := strings.LastIndexByte(script, '\'')
	if start < 0 || end <= start {
		return ""
	}
	return script[start+1 : end]
}

func TestMaildirSinkFetch(t *testing.T) {
	exec := &fakeExecer{
		files: map[string][]byte{
			"/mail/a/new/1": []byte("message one"),
			"/mail/a/cur/2": []byte("message two"),
		},
		dirs: map[string][]string{
			"/mail/a/new": {"1"},
			"/mail/a/cur": {"2"},
		},
	}
	sink := MaildirSink{Exec: exec, Dir: func(recipient string) string { return "/mail/a" }}

	msgs, err := sink.Fetch(context.Background(), "a@example.test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	got := map[string]bool{}
	for _, m := range msgs {
		got[string(m.Raw)] = true
	}
	if !got["message one"] || !got["message two"] {
		t.Errorf("messages = %v, missing expected content", msgs)
	}
}

func TestMaildirSinkFetchEmpty(t *testing.T) {
	exec := &fakeExecer{files: map[string][]byte{}, dirs: map[string][]string{}}
	sink := MaildirSink{Exec: exec, Dir: func(recipient string) string { return "/mail/empty" }}

	msgs, err := sink.Fetch(context.Background(), "nobody@example.test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

func TestMaildirSinkFetchDeterministicOrder(t *testing.T) {
	exec := &fakeExecer{
		files: map[string][]byte{
			"/mail/a/cur/1785678065.z": []byte("third"),
			"/mail/a/new/1785678063.x": []byte("first"),
			"/mail/a/new/1785678064.y": []byte("second"),
		},
		dirs: map[string][]string{
			"/mail/a/new": {"1785678064.y", "1785678063.x"}, // deliberately out of order
			"/mail/a/cur": {"1785678065.z"},
		},
	}
	sink := MaildirSink{Exec: exec, Dir: func(recipient string) string { return "/mail/a" }}

	for i := range 5 {
		msgs, err := sink.Fetch(context.Background(), "a@example.test")
		if err != nil {
			t.Fatalf("Fetch iteration %d: %v", i, err)
		}
		if len(msgs) != 3 {
			t.Fatalf("iteration %d: got %d messages, want 3", i, len(msgs))
		}
		want := []string{"first", "second", "third"}
		for j, w := range want {
			if string(msgs[j].Raw) != w {
				t.Fatalf("iteration %d: msgs[%d] = %q, want %q (order must be deterministic by filename)", i, j, msgs[j].Raw, w)
			}
		}
	}
}

func TestMaildirSinkReset(t *testing.T) {
	exec := &fakeExecer{
		files: map[string][]byte{"/mail/a/new/1": []byte("x")},
		dirs:  map[string][]string{"/mail/a/new": {"1"}},
	}
	sink := MaildirSink{Exec: exec, Dir: func(recipient string) string { return "/mail/a" }}

	if err := sink.Reset(context.Background(), "a@example.test"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	msgs, err := sink.Fetch(context.Background(), "a@example.test")
	if err != nil {
		t.Fatalf("Fetch after Reset: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages after Reset, want 0", len(msgs))
	}
}
