package smtpserver

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func testSpoolOptions(t *testing.T) spoolOptions {
	t.Helper()
	return spoolOptions{
		MaxBytes:            1024,
		MaxMemoryBytes:      16,
		MaxTotalBytes:       4096,
		MaxTotalMemoryBytes: 64,
		MaxConcurrent:       4,
		Dir:                 t.TempDir(),
	}
}

func TestSpoolStartsInMemoryAndSpills(t *testing.T) {
	opts := testSpoolOptions(t)
	manager, err := newSpoolManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	spool, err := manager.newSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()

	if _, err := io.WriteString(spool, "small"); err != nil {
		t.Fatal(err)
	}
	if spool.file != nil {
		t.Fatal("small spool unexpectedly spilled")
	}
	if _, err := io.WriteString(spool, strings.Repeat("x", 32)); err != nil {
		t.Fatal(err)
	}
	if spool.file == nil {
		t.Fatal("large spool did not spill")
	}
	info, err := spool.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("spool mode = %04o, want 0600", got)
	}
	if runtime.GOOS != "windows" {
		entries, err := os.ReadDir(opts.Dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("spool directory contains %d entries after unlink-on-open", len(entries))
		}
	}

	reader, err := spool.Reader()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	want := "small" + strings.Repeat("x", 32)
	if string(got) != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if spool.Size() != int64(len(want)) {
		t.Fatalf("size = %d, want %d", spool.Size(), len(want))
	}
}

func TestSpoolStorageFailureDoesNotLeakReservation(t *testing.T) {
	opts := testSpoolOptions(t)
	opts.MaxMemoryBytes = 1
	opts.Dir = filepath.Join(t.TempDir(), "missing")
	manager, err := newSpoolManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	spool, err := manager.newSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()

	if _, err := io.WriteString(spool, "xx"); err == nil {
		t.Fatal("write unexpectedly succeeded with missing spool directory")
	}
	if total, memory, concurrent := manager.usage(); total != 0 || memory != 0 || concurrent != 1 {
		t.Fatalf("usage = (%d, %d, %d), want (0, 0, 1)", total, memory, concurrent)
	}
}

func TestSpoolPerMessageLimitDoesNotReserve(t *testing.T) {
	opts := testSpoolOptions(t)
	opts.MaxBytes = 4
	manager, _ := newSpoolManager(opts)
	spool, _ := manager.newSpool()
	defer spool.Close()
	if _, err := io.WriteString(spool, "12345"); !errors.Is(err, errSpoolMessageTooLarge) {
		t.Fatalf("write error = %v, want errSpoolMessageTooLarge", err)
	}
	if total, memory, concurrent := manager.usage(); total != 0 || memory != 0 || concurrent != 1 {
		t.Fatalf("usage = (%d, %d, %d), want (0, 0, 1)", total, memory, concurrent)
	}
}

func TestSpoolAggregateReservationIsIncremental(t *testing.T) {
	opts := testSpoolOptions(t)
	opts.MaxTotalBytes = 6
	manager, _ := newSpoolManager(opts)
	a, _ := manager.newSpool()
	b, _ := manager.newSpool()
	defer a.Close()
	defer b.Close()
	if _, err := io.WriteString(a, "1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(b, "56"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(b, "7"); !errors.Is(err, errSpoolTotalExhausted) {
		t.Fatalf("write error = %v, want errSpoolTotalExhausted", err)
	}
	if total, _, _ := manager.usage(); total != 6 {
		t.Fatalf("total = %d, want 6", total)
	}
}

func TestSpoolAggregateMemoryPressureSpills(t *testing.T) {
	opts := testSpoolOptions(t)
	opts.MaxTotalMemoryBytes = 4
	manager, _ := newSpoolManager(opts)
	a, _ := manager.newSpool()
	b, _ := manager.newSpool()
	defer a.Close()
	defer b.Close()
	if _, err := io.WriteString(a, "1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(b, "5"); err != nil {
		t.Fatal(err)
	}
	if b.file == nil {
		t.Fatal("memory pressure did not spill second spool")
	}
	if total, memory, _ := manager.usage(); total != 5 || memory != 4 {
		t.Fatalf("usage = (%d, %d), want (5, 4)", total, memory)
	}
}

func TestSpoolConcurrentLimitAndRelease(t *testing.T) {
	opts := testSpoolOptions(t)
	opts.MaxConcurrent = 1
	manager, _ := newSpoolManager(opts)
	first, err := manager.newSpool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.newSpool(); !errors.Is(err, errSpoolConcurrentExhausted) {
		t.Fatalf("second spool error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := manager.newSpool()
	if err != nil {
		t.Fatalf("new spool after release: %v", err)
	}
	_ = second.Close()
	if total, memory, concurrent := manager.usage(); total != 0 || memory != 0 || concurrent != 0 {
		t.Fatalf("usage after release = (%d, %d, %d)", total, memory, concurrent)
	}
}

func TestSpoolCloseRemovesWindowsStyleNamedFile(t *testing.T) {
	opts := testSpoolOptions(t)
	manager, _ := newSpoolManager(opts)
	spool, _ := manager.newSpool()
	path := filepath.Join(opts.Dir, "named-spool")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	spool.file = file
	spool.path = path
	if err := spool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("named spool remains after close: %v", err)
	}
}

func TestNewSpoolManagerReportsEveryUnsetLimit(t *testing.T) {
	_, err := newSpoolManager(spoolOptions{})
	if err == nil {
		t.Fatal("newSpoolManager accepted all-zero options")
	}
	for _, field := range []string{"MaxBytes", "MaxMemoryBytes", "MaxTotalBytes", "MaxTotalMemoryBytes", "MaxConcurrent"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error %q does not name %s", err, field)
		}
	}
}

func TestSpoolConcurrentAccounting(t *testing.T) {
	opts := testSpoolOptions(t)
	opts.MaxConcurrent = 32
	opts.MaxTotalBytes = 32 * 64
	opts.MaxTotalMemoryBytes = 32 * 64
	manager, err := newSpoolManager(opts)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < opts.MaxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			spool, err := manager.newSpool()
			if err != nil {
				t.Errorf("newSpool: %v", err)
				return
			}
			if _, err := io.WriteString(spool, strings.Repeat("x", 64)); err != nil {
				t.Errorf("write: %v", err)
			}
			if err := spool.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
	}
	wg.Wait()
	if total, memory, concurrent := manager.usage(); total != 0 || memory != 0 || concurrent != 0 {
		t.Fatalf("usage after concurrent release = (%d, %d, %d)", total, memory, concurrent)
	}
}
