package group

import (
	"strings"
	"sync"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

// failureBreaker counts, for each member of a group, the different
// destinations its connections failed for. One destination that fails many
// times counts once, since it may be down by itself; threshold different
// destinations within window trip the member.
type failureBreaker struct {
	threshold int
	window    time.Duration
	access    sync.Mutex
	failures  map[string]map[string]time.Time
	tripped   map[string]bool
}

func newFailureBreaker(threshold int, window time.Duration) *failureBreaker {
	return &failureBreaker{
		threshold: threshold,
		window:    window,
		failures:  make(map[string]map[string]time.Time),
		tripped:   make(map[string]bool),
	}
}

// fail records a failed connection of the member to the destination at now,
// and reports whether the member tripped by it.
func (b *failureBreaker) fail(member string, destination M.Socksaddr, now time.Time) bool {
	key := destinationKey(destination)
	b.access.Lock()
	defer b.access.Unlock()
	for tag, destinations := range b.failures {
		for failed, at := range destinations {
			if now.Sub(at) > b.window {
				delete(destinations, failed)
			}
		}
		if len(destinations) == 0 {
			delete(b.failures, tag)
		}
	}
	destinations := b.failures[member]
	if destinations == nil {
		destinations = make(map[string]time.Time)
		b.failures[member] = destinations
	}
	destinations[key] = now
	if len(destinations) < b.threshold {
		return false
	}
	delete(b.failures, member)
	b.tripped[member] = true
	return true
}

// isTripped reports whether the member has ever tripped. It matters before
// the group's first test round only: after it, a tripped member has no test
// history until it passes a test again.
func (b *failureBreaker) isTripped(member string) bool {
	b.access.Lock()
	defer b.access.Unlock()
	return b.tripped[member]
}

// destinationKey names the host of a destination: the same host on another
// port is the same destination.
func destinationKey(destination M.Socksaddr) string {
	if destination.IsDomain() {
		return strings.ToLower(destination.Fqdn)
	}
	return destination.Addr.Unmap().String()
}
