package smtpserver

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
)

var (
	// errSpoolMessageTooLarge means a transaction crossed its configured
	// per-message byte limit. The protocol layer maps this to 552 5.3.4.
	errSpoolMessageTooLarge = errors.New("smtpserver: message exceeds spool limit")
	// errSpoolTotalExhausted means this Server instance cannot reserve more
	// aggregate spool bytes. The protocol layer maps this to 452 4.3.1.
	errSpoolTotalExhausted = errors.New("smtpserver: aggregate spool byte limit exhausted")
	// errSpoolMemoryExhausted means spilling is required because this Server
	// instance cannot reserve more aggregate in-memory spool bytes.
	errSpoolMemoryExhausted = errors.New("smtpserver: aggregate spool memory limit exhausted")
	// errSpoolConcurrentExhausted means this Server instance reached its live
	// spool count. The protocol layer maps this to 452 4.3.1.
	errSpoolConcurrentExhausted = errors.New("smtpserver: concurrent spool limit exhausted")
)

// spoolOptions configures one Server instance's CHUNKING storage budget. The
// public Server options use the contract names from SERVER-DESIGN.md; keeping
// this manager input private avoids creating a second configuration surface.
type spoolOptions struct {
	// MaxBytes is the maximum client-supplied octet count in one transaction.
	MaxBytes int64
	// MaxMemoryBytes is the maximum prefix retained in memory by one spool.
	MaxMemoryBytes int64
	// MaxTotalBytes is the maximum live spool bytes across this Server
	// instance. It is deliberately not process-wide.
	MaxTotalBytes int64
	// MaxTotalMemoryBytes is the maximum in-memory portion of live spools
	// across this Server instance.
	MaxTotalMemoryBytes int64
	// MaxConcurrent is the maximum number of live spools in this Server
	// instance.
	MaxConcurrent int
	// Dir is the spill directory. Empty uses os.TempDir.
	Dir string
}

type spoolManager struct {
	opts spoolOptions

	mu          sync.Mutex
	total       int64
	totalMemory int64
	concurrent  int
}

func newSpoolManager(opts spoolOptions) (*spoolManager, error) {
	var problems []string
	if opts.MaxBytes <= 0 {
		problems = append(problems, "MaxBytes must be positive")
	}
	if opts.MaxMemoryBytes <= 0 {
		problems = append(problems, "MaxMemoryBytes must be positive")
	}
	if opts.MaxTotalBytes <= 0 {
		problems = append(problems, "MaxTotalBytes must be positive")
	}
	if opts.MaxTotalMemoryBytes <= 0 {
		problems = append(problems, "MaxTotalMemoryBytes must be positive")
	}
	if opts.MaxConcurrent <= 0 {
		problems = append(problems, "MaxConcurrent must be positive")
	}
	if len(problems) != 0 {
		return nil, errors.New("smtpserver: invalid spool options: " + joinProblems(problems))
	}
	if opts.Dir == "" {
		opts.Dir = os.TempDir()
	}
	return &spoolManager{opts: opts}, nil
}

func joinProblems(problems []string) string {
	var b bytes.Buffer
	for i, problem := range problems {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(problem)
	}
	return b.String()
}

func (m *spoolManager) newSpool() (*spool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.concurrent >= m.opts.MaxConcurrent {
		return nil, errSpoolConcurrentExhausted
	}
	m.concurrent++
	return &spool{manager: m}, nil
}

type spool struct {
	manager *spoolManager

	mu             sync.Mutex
	memory         bytes.Buffer
	file           *os.File
	path           string
	size           int64
	reserved       int64
	reservedMemory int64
	closed         bool
}

// spoolChunkWriter records the first storage failure but continues reporting
// successful consumption to the framer. RFC 3030 requires the server to accept
// and discard the full announced chunk before it sends a failure reply; letting
// io.CopyN observe the spool error would stop the socket read too early.
type spoolChunkWriter struct {
	spool *spool
	err   error
}

func (w *spoolChunkWriter) Write(p []byte) (int, error) {
	if w.err == nil {
		if _, err := w.spool.Write(p); err != nil {
			w.err = err
			// The framer already obtained all of p from the peer. Returning its
			// full length makes that consumption authoritative even when only a
			// prefix reached storage.
		}
	}
	return len(p), nil
}

func (w *spoolChunkWriter) Err() error { return w.err }

func (s *spool) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("smtpserver: write to closed spool")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > s.manager.opts.MaxBytes-s.size {
		return 0, errSpoolMessageTooLarge
	}

	if s.file == nil && s.size+int64(len(p)) > s.manager.opts.MaxMemoryBytes {
		if err := s.spillLocked(); err != nil {
			return 0, err
		}
	}

	memory := s.file == nil
	if err := s.manager.reserve(int64(len(p)), boolBytes(memory, int64(len(p)))); err != nil {
		if memory && errors.Is(err, errSpoolMemoryExhausted) {
			if spillErr := s.spillLocked(); spillErr != nil {
				return 0, spillErr
			}
			memory = false
			if err = s.manager.reserve(int64(len(p)), 0); err != nil {
				return 0, err
			}
		} else {
			return 0, err
		}
	}

	var n int
	var err error
	if s.file == nil {
		n, err = s.memory.Write(p)
	} else {
		n, err = s.file.Write(p)
	}
	if n > 0 {
		s.size += int64(n)
		s.reserved += int64(n)
		if memory {
			s.reservedMemory += int64(n)
		}
	}
	if unused := int64(len(p) - n); unused > 0 {
		s.manager.release(unused, boolBytes(memory, unused))
	}
	if err != nil {
		return n, fmt.Errorf("smtpserver: write spool: %w", err)
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func boolBytes(memory bool, n int64) int64 {
	if memory {
		return n
	}
	return 0
}

func (s *spool) spillLocked() error {
	if s.file != nil {
		return nil
	}
	file, path, err := createUnlinkedSpoolFile(s.manager.opts.Dir)
	if err != nil {
		return fmt.Errorf("smtpserver: create spool file: %w", err)
	}
	if s.memory.Len() > 0 {
		if _, err := file.Write(s.memory.Bytes()); err != nil {
			_ = file.Close()
			return fmt.Errorf("smtpserver: spill spool: %w", err)
		}
		s.manager.release(0, s.reservedMemory)
		s.reservedMemory = 0
	}
	s.memory.Reset()
	s.file = file
	s.path = path
	return nil
}

func createUnlinkedSpoolFile(dir string) (*os.File, string, error) {
	// os.CreateTemp opens with O_CREATE|O_EXCL and mode 0600.
	file, err := os.CreateTemp(dir, ".go-smtp-spool-*")
	if err != nil {
		return nil, "", err
	}
	path := file.Name()
	if runtime.GOOS != "windows" {
		if err := os.Remove(path); err != nil {
			_ = file.Close()
			return nil, "", err
		}
		return file, "", nil
	}
	return file, path, nil
}

func (s *spool) Reader() (io.ReadSeeker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("smtpserver: read closed spool")
	}
	if s.file == nil {
		return bytes.NewReader(s.memory.Bytes()), nil
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("smtpserver: rewind spool: %w", err)
	}
	return s.file, nil
}

func (s *spool) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

func (s *spool) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	file := s.file
	path := s.path
	total := s.reserved
	memory := s.reservedMemory
	s.reserved = 0
	s.reservedMemory = 0
	s.mu.Unlock()

	var err error
	if file != nil {
		err = file.Close()
	}
	if path != "" {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = removeErr
		}
	}
	s.manager.releaseSpool(total, memory)
	return err
}

func (m *spoolManager) reserve(total, memory int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if total > m.opts.MaxTotalBytes-m.total {
		return errSpoolTotalExhausted
	}
	if memory > m.opts.MaxTotalMemoryBytes-m.totalMemory {
		return errSpoolMemoryExhausted
	}
	m.total += total
	m.totalMemory += memory
	return nil
}

func (m *spoolManager) release(total, memory int64) {
	m.mu.Lock()
	m.total -= total
	m.totalMemory -= memory
	m.mu.Unlock()
}

func (m *spoolManager) releaseSpool(total, memory int64) {
	m.mu.Lock()
	m.total -= total
	m.totalMemory -= memory
	m.concurrent--
	m.mu.Unlock()
}

func (m *spoolManager) usage() (total, memory int64, concurrent int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total, m.totalMemory, m.concurrent
}
