package harness

import "time"

// pollInterval governs how often WaitForMessage and container health checks
// re-poll. It is a var, not a const, so the harness's own unit tests can
// shrink it instead of depending on wall-clock time.
var pollInterval = 200 * time.Millisecond

func defaultPollTick() <-chan time.Time {
	return time.After(pollInterval)
}
