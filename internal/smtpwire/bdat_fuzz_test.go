package smtpwire

import (
	"bytes"
	"errors"
	"io"
	"math"
	"runtime"
	"testing"
)

// FuzzBDATFraming checks that a server-controlled size can never cause an
// oversized copy, integer conversion, or partially framed BDAT command.
func FuzzBDATFraming(f *testing.F) {
	f.Add(uint64(0), false, []byte(""))
	f.Add(uint64(3), false, []byte("abc"))
	f.Add(uint64(3), true, []byte("abc"))
	f.Add(uint64(4), false, []byte("abc")) // short source
	f.Add(uint64(math.MaxUint32), true, []byte("x"))
	f.Add(uint64(math.MaxUint64), false, []byte("x"))

	f.Fuzz(func(t *testing.T, size uint64, last bool, source []byte) {
		limits := Limits{MaxBDATChunkSize: 1024}
		var command bytes.Buffer
		err := EncodeBDATCommand(&command, size, last, limits)
		if size > 1024 {
			if err == nil || command.Len() != 0 {
				t.Fatalf("oversized BDAT %d wrote %q, err=%v", size, command.Bytes(), err)
			}
			return
		}
		if err != nil {
			t.Fatalf("EncodeBDATCommand(%d): %v", size, err)
		}

		var body bytes.Buffer
		n, err := CopyBDATChunk(&body, bytes.NewReader(source), size, limits)
		if uint64(len(source)) < size {
			if !errors.Is(err, ErrBDATShortSource) || n != int64(len(source)) {
				t.Fatalf("short source len=%d size=%d: n=%d err=%v", len(source), size, n, err)
			}
			return
		}
		if err != nil || n != int64(size) || !bytes.Equal(body.Bytes(), source[:int(size)]) {
			t.Fatalf("CopyBDATChunk len=%d size=%d: n=%d body=%q err=%v", len(source), size, n, body.Bytes(), err)
		}
	})
}

// TestStreamingTransparencyAndBDATMemory is deliberately in the hardening
// suite: both paths must keep a 200 MiB message streaming rather than turning
// server-controlled transfer sizes into an in-memory message buffer.
func TestStreamingTransparencyAndBDATMemory(t *testing.T) {
	const messageSize = int64(200 << 20)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	dot := NewDotStuffWriter(io.Discard)
	if _, err := io.Copy(dot, &zeroReader{remaining: messageSize}); err != nil {
		t.Fatal(err)
	}
	if err := dot.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := CopyBDATChunk(io.Discard, &zeroReader{remaining: messageSize}, uint64(messageSize), Limits{MaxBDATChunkSize: messageSize}); err != nil || n != messageSize {
		t.Fatalf("CopyBDATChunk: n=%d err=%v", n, err)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.Alloc > before.Alloc && after.Alloc-before.Alloc > 8<<20 {
		t.Fatalf("streaming 2x200 MiB retained %d bytes", after.Alloc-before.Alloc)
	}
}

type zeroReader struct{ remaining int64 }

func (r *zeroReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 0
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}
