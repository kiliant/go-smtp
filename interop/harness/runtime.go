package harness

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

// StartProfile starts p through its in-process callback or the existing
// Podman path and returns one runtime-neutral Handle. Readiness remains the
// caller's responsibility; matrix callers must run AssertProfile so startup is
// gated on a real EHLO or LHLO rather than a fixed delay.
func StartProfile(ctx context.Context, p Profile) (*Handle, error) {
	if p.Start == nil {
		run := p.Run
		run.Name = ContainerName(p.Name)
		handle, err := Run(ctx, run)
		if err != nil {
			return nil, err
		}
		handle.newSink = p.NewSink
		return handle, nil
	}

	runtime, err := p.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("harness: starting in-process profile %s: %w", p.Name, err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("harness: in-process profile %s returned a nil runtime", p.Name)
	}
	hostPorts := make(map[int]int, len(runtime.Addresses))
	hostAddrs := make(map[int]string, len(runtime.Addresses))
	for _, port := range p.Ports {
		addr, ok := runtime.Addresses[port.Container]
		if !ok || addr == "" {
			stopRuntime(ctx, runtime)
			return nil, fmt.Errorf("harness: in-process profile %s has no address for logical port %d", p.Name, port.Container)
		}
		_, portText, splitErr := net.SplitHostPort(addr)
		hostPort, parseErr := strconv.Atoi(portText)
		if splitErr != nil || parseErr != nil || hostPort < 1 || hostPort > 65535 {
			stopRuntime(ctx, runtime)
			return nil, fmt.Errorf("harness: in-process profile %s has invalid address %q for logical port %d", p.Name, addr, port.Container)
		}
		hostPorts[port.Container] = hostPort
		hostAddrs[port.Container] = addr
	}
	return &Handle{
		Name:      p.Name,
		hostPorts: hostPorts,
		hostAddrs: hostAddrs,
		sink:      runtime.Sink,
		stop:      runtime.Stop,
		logs:      runtime.Logs,
	}, nil
}

func stopRuntime(ctx context.Context, runtime *RuntimeConfig) {
	if runtime.Stop != nil {
		_ = runtime.Stop(ctx)
	}
}
