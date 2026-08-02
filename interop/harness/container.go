package harness

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

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

// Handle is a started container. Callers must call Stop (directly or via
// t.Cleanup) exactly once to release it; Stop is idempotent so a deferred
// call after an explicit one is harmless.
type Handle struct {
	Name       string
	hostPorts  map[int]int
	podmanPath string
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
	}
	args := []string{"run", "-d", "--name", cfg.Name}
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

// HostPort returns the host-side port podman assigned for containerPort.
func (h *Handle) HostPort(containerPort int) (int, bool) {
	p, ok := h.hostPorts[containerPort]
	return p, ok
}

// HostAddr returns "127.0.0.1:<hostPort>" for containerPort, the form
// smtpclient.ClientOptions.Address expects.
func (h *Handle) HostAddr(containerPort int) (string, bool) {
	p, ok := h.HostPort(containerPort)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("127.0.0.1:%d", p), true
}

// Exec runs args inside the running container and returns its stdout.
func (h *Handle) Exec(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"exec", h.Name}, args...)
	stdout, stderr, err := runPodman(ctx, h.podmanPath, full...)
	if err != nil {
		return nil, fmt.Errorf("harness: podman exec %s %v: %w: %s", h.Name, args, err, strings.TrimSpace(stderr))
	}
	return []byte(stdout), nil
}

// Logs returns the container's combined log output, for attaching to a
// failure diagnostic.
func (h *Handle) Logs(ctx context.Context) (string, error) {
	stdout, stderr, err := runPodman(ctx, h.podmanPath, "logs", h.Name)
	if err != nil {
		return "", fmt.Errorf("harness: podman logs %s: %w: %s", h.Name, err, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// Stop removes the container, ignoring "no such container" so repeated or
// deferred-after-explicit calls are safe.
func (h *Handle) Stop(ctx context.Context) error {
	if h == nil || h.podmanPath == "" {
		return nil
	}
	_, stderr, err := runPodman(ctx, h.podmanPath, "rm", "-f", h.Name)
	if err != nil && !strings.Contains(stderr, "no such container") {
		return fmt.Errorf("harness: podman rm %s: %w: %s", h.Name, err, strings.TrimSpace(stderr))
	}
	return nil
}
