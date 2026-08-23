package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestPrepareImageRetriesTransientPullFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman helper is a POSIX shell script")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "podman.log")
	sentinelPath := filepath.Join(dir, "first-pull-failed")
	podmanPath := filepath.Join(dir, "podman")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_PODMAN_LOG"
if [ "$1" = image ] && [ "$2" = exists ]; then
	exit 1
fi
if [ "$1" = pull ] && [ ! -e "$FAKE_PODMAN_SENTINEL" ]; then
	: > "$FAKE_PODMAN_SENTINEL"
	echo 'transient unpack failure' >&2
	exit 1
fi
exit 0
`
	if err := os.WriteFile(podmanPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake podman: %v", err)
	}
	t.Setenv("FAKE_PODMAN_LOG", logPath)
	t.Setenv("FAKE_PODMAN_SENTINEL", sentinelPath)

	const image = "registry.example/server@sha256:abc"
	if err := prepareImage(context.Background(), podmanPath, image); err != nil {
		t.Fatalf("prepareImage: %v", err)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading fake podman log: %v", err)
	}
	want := strings.Join([]string{
		"image exists " + image,
		"pull " + image,
		"pull " + image,
		"",
	}, "\n")
	if string(log) != want {
		t.Fatalf("podman calls:\n%s\nwant:\n%s", log, want)
	}
}

func TestPrepareImageUsesCachedImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman helper is a POSIX shell script")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "podman.log")
	podmanPath := filepath.Join(dir, "podman")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_PODMAN_LOG"
if [ "$1" = image ] && [ "$2" = exists ]; then
	exit 0
fi
exit 99
`
	if err := os.WriteFile(podmanPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake podman: %v", err)
	}
	t.Setenv("FAKE_PODMAN_LOG", logPath)

	const image = "registry.example/server@sha256:def"
	if err := prepareImage(context.Background(), podmanPath, image); err != nil {
		t.Fatalf("prepareImage: %v", err)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading fake podman log: %v", err)
	}
	want := "image exists " + image + "\n"
	if string(log) != want {
		t.Fatalf("podman calls: %q; want %q", log, want)
	}
}

func TestPrepareImageSerializesImageStoreAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman helper is a POSIX shell script")
	}

	dir := t.TempDir()
	guardPath := filepath.Join(dir, "image-store-guard")
	overlapPath := filepath.Join(dir, "overlap")
	podmanPath := filepath.Join(dir, "podman")
	script := `#!/bin/sh
if ! mkdir "$FAKE_PODMAN_GUARD" 2>/dev/null; then
	: > "$FAKE_PODMAN_OVERLAP"
	exit 0
fi
sleep 0.1
rmdir "$FAKE_PODMAN_GUARD"
exit 0
`
	if err := os.WriteFile(podmanPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake podman: %v", err)
	}
	t.Setenv("FAKE_PODMAN_GUARD", guardPath)
	t.Setenv("FAKE_PODMAN_OVERLAP", overlapPath)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, image := range []string{"registry.example/one:latest", "registry.example/two:latest"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- prepareImage(context.Background(), podmanPath, image)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("prepareImage: %v", err)
		}
	}
	if _, err := os.Stat(overlapPath); err == nil {
		t.Fatal("concurrent prepareImage calls overlapped in the image store")
	} else if !os.IsNotExist(err) {
		t.Fatalf("checking overlap marker: %v", err)
	}
}
