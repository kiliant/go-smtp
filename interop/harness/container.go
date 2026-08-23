package harness

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const imagePullAttempts = 3

// imageStoreMu serializes operations that populate Podman's shared image
// store. Matrix profiles start in parallel, but concurrent cold pulls and
// builds can race in the storage driver and leave a layer only partly
// unpacked. Container startup and health checks remain parallel once each
// image is ready.
var imageStoreMu sync.Mutex

// RunConfig describes one container to start. Construct with keyed fields:
// new fields (health-check commands, resource limits) are expected as more
// server profiles are added.
type RunConfig struct {
	// Name is the container name; use ContainerName to build one.
	Name string
	// Image is a pinned image reference. Exactly one of Image or
	// ContainerfileDir must be set.
	Image string
	// ContainerfileDir builds a local image from a Containerfile before
	// running it (used for servers with no usable published multi-arch
	// image, e.g. Exim).
	ContainerfileDir string
	// Env is the container's environment.
	Env map[string]string
	// Ports are the container-side ports to publish; the host side is
	// chosen by podman and recovered with Handle.HostPort.
	Ports []int
	// CapAdd lists extra Linux capabilities to grant the container, e.g.
	// "NET_BIND_SERVICE" for a server that must bind a port under 1024
	// (LMTP's conventional port 24) while running as a non-root image
	// user.
	CapAdd []string

	_ struct{}
}

// Handle is one started profile runtime. Callers must call Stop (directly or
// via t.Cleanup) exactly once to release it; Stop is idempotent so a deferred
// call after an explicit one is harmless.
type Handle struct {
	Name       string
	hostPorts  map[int]int
	hostAddrs  map[int]string
	podmanPath string
	sink       Sink
	newSink    func(ctx context.Context, h *Handle) (Sink, error)
	stop       func(ctx context.Context) error
	logs       func(ctx context.Context) (string, error)
	stopOnce   sync.Once
	stopErr    error
}

// podmanBinary returns the resolved podman executable, or an error
// classified as environmental — the harness cannot proceed without it, and
// that is a property of the local machine, not of the client or server under
// test.
func podmanBinary() (string, error) {
	path, err := exec.LookPath("podman")
	if err != nil {
		return "", fmt.Errorf("harness: podman not found in PATH: %w", err)
	}
	return path, nil
}

func runPodman(ctx context.Context, podman string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, podman, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

// BuildImage builds dir's Containerfile and tags it tag. It is used for
// servers with no maintained multi-arch published image.
func BuildImage(ctx context.Context, dir, tag string) error {
	imageStoreMu.Lock()
	defer imageStoreMu.Unlock()

	podman, err := podmanBinary()
	if err != nil {
		return err
	}
	_, stderr, err := runPodman(ctx, podman, "build", "-t", tag, dir)
	if err != nil {
		return fmt.Errorf("harness: podman build %s: %w: %s", dir, err, strings.TrimSpace(stderr))
	}
	return nil
}

// prepareImage makes a published image available before podman run. Pulling
// explicitly lets the harness retry transient registry/storage failures and
// prevents podman run from starting an implicit pull alongside another
// profile's build.
func prepareImage(ctx context.Context, podman, image string) error {
	imageStoreMu.Lock()
	defer imageStoreMu.Unlock()

	if _, _, err := runPodman(ctx, podman, "image", "exists", image); err == nil {
		return nil
	}

	var (
		attempts int
		stderr   string
		err      error
	)
	for attempts < imagePullAttempts {
		attempts++
		_, stderr, err = runPodman(ctx, podman, "pull", image)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("harness: podman pull %s after %d attempts: %w: %s",
		image, attempts, err, strings.TrimSpace(stderr))
}

// Run starts a container per cfg and returns a Handle once podman reports it
// running. It does not wait for the service inside to become ready — use
// WaitForEHLO or a Sink read for that.
func Run(ctx context.Context, cfg RunConfig) (*Handle, error) {
	podman, err := podmanBinary()
	if err != nil {
		return nil, err
	}
	if cfg.Image == "" && cfg.ContainerfileDir == "" {
		return nil, fmt.Errorf("harness: RunConfig for %s needs Image or ContainerfileDir", cfg.Name)
	}
	image := cfg.Image
	if cfg.ContainerfileDir != "" {
		image = "go-smtp-interop/" + cfg.Name
		if err := BuildImage(ctx, cfg.ContainerfileDir, image); err != nil {
			return nil, err
		}
	} else if err := prepareImage(ctx, podman, image); err != nil {
		return nil, err
	}
	args := []string{"run", "--pull=never", "-d", "--name", cfg.Name}
	for _, cap := range cfg.CapAdd {
		args = append(args, "--cap-add="+cap)
	}
	for _, p := range cfg.Ports {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1::%d", p))
	}
	for k, v := range cfg.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, image)
	_, stderr, err := runPodman(ctx, podman, args...)
	if err != nil {
		return nil, fmt.Errorf("harness: podman run %s: %w: %s", cfg.Name, err, strings.TrimSpace(stderr))
	}
	h := &Handle{Name: cfg.Name, podmanPath: podman}
	ports, err := h.resolvePorts(ctx, cfg.Ports)
	if err != nil {
		_ = h.Stop(context.Background())
		return nil, err
	}
	h.hostPorts = ports
	return h, nil
}

var portLinePattern = regexp.MustCompile(`:(\d+)\s*$`)

// resolvePorts queries "podman port" for each requested container port and
// records the host port podman actually assigned. "podman port" can print
// more than one mapping line for a single container port (e.g. separate
// IPv4/IPv6 bindings), so each line is parsed independently and only the
// first is used, rather than matching the port pattern against the whole
// (possibly multi-line) output — which would silently anchor to the last
// line only, regardless of which address it belonged to.
func (h *Handle) resolvePorts(ctx context.Context, containerPorts []int) (map[int]int, error) {
	out := make(map[int]int, len(containerPorts))
	for _, cp := range containerPorts {
		stdout, stderr, err := runPodman(ctx, h.podmanPath, "port", h.Name, strconv.Itoa(cp))
		if err != nil {
			return nil, fmt.Errorf("harness: podman port %s %d: %w: %s", h.Name, cp, err, strings.TrimSpace(stderr))
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
			return nil, fmt.Errorf("harness: no port mapping printed for %s port %d", h.Name, cp)
		}
		line := strings.TrimSpace(lines[0])
		m := portLinePattern.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("harness: could not parse host port from %q", line)
		}
		hp, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("harness: invalid host port %q: %w", m[1], err)
		}
		out[cp] = hp
	}
	return out, nil
}

// HostPort returns the host-side port assigned for logicalPort.
func (h *Handle) HostPort(containerPort int) (int, bool) {
	p, ok := h.hostPorts[containerPort]
	return p, ok
}

// HostAddr returns the dialable address for logicalPort, in the form
// smtpclient.ClientOptions.Address expects.
func (h *Handle) HostAddr(containerPort int) (string, bool) {
	if addr, ok := h.hostAddrs[containerPort]; ok {
		return addr, true
	}
	p, ok := h.HostPort(containerPort)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("127.0.0.1:%d", p), true
}

// NewSink returns the retrieval sink attached to this runtime. Container
// profiles construct theirs lazily against the started Handle; in-process
// profiles supply an already-bound sink with RuntimeConfig.
func (h *Handle) NewSink(ctx context.Context) (Sink, error) {
	if h == nil {
		return nil, nil
	}
	if h.sink != nil {
		return h.sink, nil
	}
	if h.newSink != nil {
		return h.newSink(ctx, h)
	}
	return nil, nil
}

// Exec runs args inside the running container and returns its stdout.
func (h *Handle) Exec(ctx context.Context, args ...string) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("harness: nil profile runtime does not support exec")
	}
	if h.podmanPath == "" {
		return nil, fmt.Errorf("harness: profile runtime %q does not support exec", h.Name)
	}
	full := append([]string{"exec", h.Name}, args...)
	stdout, stderr, err := runPodman(ctx, h.podmanPath, full...)
	if err != nil {
		return nil, fmt.Errorf("harness: podman exec %s %v: %w: %s", h.Name, args, err, strings.TrimSpace(stderr))
	}
	return []byte(stdout), nil
}

// Logs returns the runtime's diagnostic output. Containers return their
// combined Podman logs; an in-process profile may supply a callback.
func (h *Handle) Logs(ctx context.Context) (string, error) {
	if h == nil {
		return "", nil
	}
	if h.logs != nil {
		return h.logs(ctx)
	}
	if h.podmanPath == "" {
		return "", nil
	}
	stdout, stderr, err := runPodman(ctx, h.podmanPath, "logs", h.Name)
	if err != nil {
		return "", fmt.Errorf("harness: podman logs %s: %w: %s", h.Name, err, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// Stop releases the runtime. For containers it removes the container,
// ignoring "no such container" so repeated or deferred-after-explicit calls
// are safe.
func (h *Handle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.stopOnce.Do(func() {
		if h.stop != nil {
			h.stopErr = h.stop(ctx)
			return
		}
		if h.podmanPath == "" {
			return
		}
		_, stderr, err := runPodman(ctx, h.podmanPath, "rm", "-f", h.Name)
		if err != nil && !strings.Contains(stderr, "no such container") {
			h.stopErr = fmt.Errorf("harness: podman rm %s: %w: %s", h.Name, err, strings.TrimSpace(stderr))
		}
	})
	return h.stopErr
}
