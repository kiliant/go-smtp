package harness

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

// counter makes container names unique across profiles started by the same
// process within one wall-clock second, which PID+timestamp alone does not
// guarantee.
var counter atomic.Uint64

// ContainerName builds a container name embedding the process ID, a
// nanosecond timestamp and a per-process counter, so two test processes (or
// two profiles in the same process) starting the same server within one
// wall-clock second never collide. This was a hard failure in the sibling
// IMAP harness (identical names within one second made podman run fail
// outright); the composition is load-bearing, not cosmetic.
func ContainerName(profile string) string {
	n := counter.Add(1)
	return fmt.Sprintf("go-smtp-interop-%s-%d-%d-%d", profile, os.Getpid(), time.Now().UnixNano(), n)
}
