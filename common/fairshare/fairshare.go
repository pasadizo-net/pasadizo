// Package fairshare shares a fixed bandwidth fairly between source addresses.
//
// Each direction is a single shaper running at the configured rate. While the
// shaper has spare capacity, traffic passes without waiting. Once it is
// backlogged, transfers are released in start-time fair queueing order keyed
// by the source address, so every active source gets an equal share and the
// share of an idle source goes to the others.
//
// Shaping happens after a transfer is reported, through the counter hooks of
// the copy loop, so the copy fast paths stay in place and backpressure reaches
// the sender through the transport's own flow control.
package fairshare

import (
	"container/heap"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
)

// burst is how far the shaper may fall behind real time before the unused
// capacity is forgotten. It absorbs timer slack and short idle gaps.
const burst = 20 * time.Millisecond

// sweepInterval is how many queued transfers are released between sweeps of
// the per-source state.
const sweepInterval = 1024

type Limiter struct {
	upload   *shaper
	download *shaper
}

// NewLimiter creates a limiter shared by every connection it wraps. A rate of
// zero leaves that direction unlimited.
func NewLimiter(uploadBytesPerSecond uint64, downloadBytesPerSecond uint64) *Limiter {
	return &Limiter{
		upload:   newShaper(uploadBytesPerSecond),
		download: newShaper(downloadBytesPerSecond),
	}
}

// NewConn wraps the client side of a connection: bytes read from it count as
// upload and bytes written to it as download.
func (l *Limiter) NewConn(conn net.Conn, source netip.Addr) net.Conn {
	return bufio.NewCounterConn(conn, l.upload.counters(source), l.download.counters(source))
}

// NewPacketConn wraps the client side of a packet connection.
func (l *Limiter) NewPacketConn(conn N.PacketConn, source netip.Addr) N.PacketConn {
	return bufio.NewCounterPacketConn(conn, l.upload.counters(source), l.download.counters(source))
}

type shaper struct {
	bytesPerSecond int64
	access         sync.Mutex
	// next is the time at which the transfers released so far have been paid for.
	next        time.Time
	virtualTime uint64
	sequence    uint64
	granted     uint64
	sources     map[netip.Addr]*sourceState
	queue       waiterQueue
	running     bool
}

type sourceState struct {
	finish  uint64
	pending int
}

type waiter struct {
	source   netip.Addr
	start    uint64
	sequence uint64
	n        int64
	done     chan struct{}
}

func newShaper(bytesPerSecond uint64) *shaper {
	if bytesPerSecond == 0 {
		return nil
	}
	return &shaper{
		bytesPerSecond: int64(bytesPerSecond),
		sources:        make(map[netip.Addr]*sourceState),
	}
}

func (s *shaper) counters(source netip.Addr) []N.CountFunc {
	if s == nil {
		return nil
	}
	source = source.Unmap()
	return []N.CountFunc{func(n int64) {
		s.wait(source, n)
	}}
}

func (s *shaper) cost(n int64) time.Duration {
	return time.Duration(n * int64(time.Second) / s.bytesPerSecond)
}

// pace moves the shaper clock forward to now minus the burst allowance, so
// capacity left unused for longer than that is not saved up.
func (s *shaper) pace(now time.Time) {
	earliest := now.Add(-burst)
	if s.next.Before(earliest) {
		s.next = earliest
	}
}

func (s *shaper) wait(source netip.Addr, n int64) {
	if n <= 0 {
		return
	}
	s.access.Lock()
	if s.queue.Len() == 0 {
		now := time.Now()
		s.pace(now)
		if !s.next.After(now) {
			s.next = s.next.Add(s.cost(n))
			s.access.Unlock()
			return
		}
	}
	state := s.sources[source]
	if state == nil {
		state = new(sourceState)
		s.sources[source] = state
	}
	start := max(s.virtualTime, state.finish)
	state.finish = start + uint64(n)
	state.pending++
	s.sequence++
	current := &waiter{
		source:   source,
		start:    start,
		sequence: s.sequence,
		n:        n,
		done:     make(chan struct{}),
	}
	heap.Push(&s.queue, current)
	if !s.running {
		s.running = true
		go s.dispatch()
	}
	s.access.Unlock()
	<-current.done
}

// sweep forgets the sources that have nothing queued and whose finish tag
// the virtual time has passed: they would start at the virtual time anyway.
func (s *shaper) sweep() {
	for source, state := range s.sources {
		if state.pending == 0 && state.finish <= s.virtualTime {
			delete(s.sources, source)
		}
	}
}

func (s *shaper) dispatch() {
	s.access.Lock()
	for {
		if s.queue.Len() == 0 {
			// Nothing is backlogged, so no finish tag matters any more.
			clear(s.sources)
			s.running = false
			s.access.Unlock()
			return
		}
		now := time.Now()
		s.pace(now)
		if delay := s.next.Sub(now); delay > 0 {
			s.access.Unlock()
			time.Sleep(delay)
			s.access.Lock()
			continue
		}
		current := heap.Pop(&s.queue).(*waiter)
		s.virtualTime = current.start
		s.sources[current.source].pending--
		s.granted++
		if s.granted%sweepInterval == 0 {
			s.sweep()
		}
		s.next = s.next.Add(s.cost(current.n))
		close(current.done)
	}
}

type waiterQueue []*waiter

func (q waiterQueue) Len() int {
	return len(q)
}

func (q waiterQueue) Less(i, j int) bool {
	if q[i].start != q[j].start {
		return q[i].start < q[j].start
	}
	return q[i].sequence < q[j].sequence
}

func (q waiterQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

func (q *waiterQueue) Push(x any) {
	*q = append(*q, x.(*waiter))
}

func (q *waiterQueue) Pop() any {
	old := *q
	last := old[len(old)-1]
	old[len(old)-1] = nil
	*q = old[:len(old)-1]
	return last
}
