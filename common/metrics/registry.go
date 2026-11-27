// Package metrics counts inbound connections and outbound dials for the
// Prometheus endpoints. A nil *Registry is valid and records nothing, so
// callers do not need to check whether metrics are enabled.
package metrics

import (
	"context"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

// DialDurationBuckets are the upper bounds, in seconds, of the dial duration
// histogram.
var DialDurationBuckets = [...]float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

var dialBucketBounds = func() (bounds [len(DialDurationBuckets)]time.Duration) {
	for i, bound := range DialDurationBuckets {
		bounds[i] = time.Duration(bound * float64(time.Second))
	}
	return
}()

type Registry struct {
	inbounds  sync.Map // inboundKey -> *atomic.Uint64
	errors    sync.Map // string -> *atomic.Uint64
	outbounds sync.Map // string -> *dialStatistics
}

type inboundKey struct {
	inbound string
	network string
}

type dialStatistics struct {
	dials   atomic.Uint64
	errors  atomic.Uint64
	buckets [len(dialBucketBounds) + 1]atomic.Uint64 // the last one is +Inf
	sumNano atomic.Int64
}

func NewRegistry() *Registry {
	return &Registry{}
}

// FromContext returns the registry of the box, or nil when metrics are off.
func FromContext(ctx context.Context) *Registry {
	return service.PtrFromContext[Registry](ctx)
}

func loadCounter[K comparable](counters *sync.Map, key K) *atomic.Uint64 {
	if counter, loaded := counters.Load(key); loaded {
		return counter.(*atomic.Uint64)
	}
	counter, _ := counters.LoadOrStore(key, new(atomic.Uint64))
	return counter.(*atomic.Uint64)
}

func (r *Registry) loadDial(tag string) *dialStatistics {
	if statistics, loaded := r.outbounds.Load(tag); loaded {
		return statistics.(*dialStatistics)
	}
	statistics, _ := r.outbounds.LoadOrStore(tag, new(dialStatistics))
	return statistics.(*dialStatistics)
}

// InboundConnection counts a connection accepted by an inbound.
func (r *Registry) InboundConnection(inbound string, network string) {
	if r == nil {
		return
	}
	loadCounter(&r.inbounds, inboundKey{inbound, network}).Add(1)
}

// InboundError counts an inbound connection that failed before it was
// established. Closed and canceled connections are not errors.
func (r *Registry) InboundError(inbound string, err error) {
	if r == nil || err == nil || E.IsClosedOrCanceled(err) {
		return
	}
	loadCounter(&r.errors, inbound).Add(1)
}

// ObserveDial records one dial through the outbound with the tag.
func (r *Registry) ObserveDial(tag string, start time.Time, err error) {
	if r == nil {
		return
	}
	duration := time.Since(start)
	statistics := r.loadDial(tag)
	statistics.dials.Add(1)
	if err != nil {
		statistics.errors.Add(1)
	}
	bucket := len(dialBucketBounds)
	for i, bound := range dialBucketBounds {
		if duration <= bound {
			bucket = i
			break
		}
	}
	statistics.buckets[bucket].Add(1)
	statistics.sumNano.Add(int64(duration))
}

// DialContext dials through the outbound and records the dial under its tag.
func (r *Registry) DialContext(ctx context.Context, outbound adapter.Outbound, network string, destination M.Socksaddr) (net.Conn, error) {
	if r == nil {
		return outbound.DialContext(ctx, network, destination)
	}
	start := time.Now()
	conn, err := outbound.DialContext(ctx, network, destination)
	r.ObserveDial(outbound.Tag(), start, err)
	return conn, err
}

// ListenPacket opens a packet connection through the outbound and records it
// as a dial under its tag.
func (r *Registry) ListenPacket(ctx context.Context, outbound adapter.Outbound, destination M.Socksaddr) (net.PacketConn, error) {
	if r == nil {
		return outbound.ListenPacket(ctx, destination)
	}
	start := time.Now()
	conn, err := outbound.ListenPacket(ctx, destination)
	r.ObserveDial(outbound.Tag(), start, err)
	return conn, err
}

// TagOf returns the tag of the dialer when it is an outbound.
func TagOf(dialer N.Dialer) (string, bool) {
	outbound, isOutbound := dialer.(adapter.Outbound)
	if !isOutbound {
		return "", false
	}
	return outbound.Tag(), true
}

type InboundSnapshot struct {
	Inbound     string
	Network     string
	Connections uint64
}

type InboundErrorSnapshot struct {
	Inbound string
	Errors  uint64
}

type DialSnapshot struct {
	Outbound string
	Dials    uint64
	Errors   uint64
	// Buckets are cumulative counts for DialDurationBuckets, then +Inf.
	Buckets []uint64
	Sum     float64
}

type Snapshot struct {
	Inbounds      []InboundSnapshot
	InboundErrors []InboundErrorSnapshot
	Dials         []DialSnapshot
}

// Snapshot returns the counters sorted by label.
func (r *Registry) Snapshot() Snapshot {
	var snapshot Snapshot
	if r == nil {
		return snapshot
	}
	r.inbounds.Range(func(key, value any) bool {
		inbound := key.(inboundKey)
		snapshot.Inbounds = append(snapshot.Inbounds, InboundSnapshot{
			Inbound:     inbound.inbound,
			Network:     inbound.network,
			Connections: value.(*atomic.Uint64).Load(),
		})
		return true
	})
	r.errors.Range(func(key, value any) bool {
		snapshot.InboundErrors = append(snapshot.InboundErrors, InboundErrorSnapshot{
			Inbound: key.(string),
			Errors:  value.(*atomic.Uint64).Load(),
		})
		return true
	})
	r.outbounds.Range(func(key, value any) bool {
		statistics := value.(*dialStatistics)
		dial := DialSnapshot{
			Outbound: key.(string),
			Buckets:  make([]uint64, len(statistics.buckets)),
			Sum:      float64(statistics.sumNano.Load()) / float64(time.Second),
		}
		// Read the buckets before the totals, so a dial that lands in
		// between can only make the totals larger than the buckets, never
		// the +Inf bucket larger than the count.
		var cumulative uint64
		for i := range statistics.buckets {
			cumulative += statistics.buckets[i].Load()
			dial.Buckets[i] = cumulative
		}
		dial.Dials = max(statistics.dials.Load(), cumulative)
		dial.Errors = statistics.errors.Load()
		snapshot.Dials = append(snapshot.Dials, dial)
		return true
	})
	sort.Slice(snapshot.Inbounds, func(i, j int) bool {
		if snapshot.Inbounds[i].Inbound != snapshot.Inbounds[j].Inbound {
			return snapshot.Inbounds[i].Inbound < snapshot.Inbounds[j].Inbound
		}
		return snapshot.Inbounds[i].Network < snapshot.Inbounds[j].Network
	})
	sort.Slice(snapshot.InboundErrors, func(i, j int) bool {
		return snapshot.InboundErrors[i].Inbound < snapshot.InboundErrors[j].Inbound
	})
	sort.Slice(snapshot.Dials, func(i, j int) bool {
		return snapshot.Dials[i].Outbound < snapshot.Dials[j].Outbound
	})
	return snapshot
}
