package group

import (
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

// A member that keeps its own transport connection (a Hysteria2 outbound
// keeps a QUIC connection) is stalled when that connection has kept sending
// packets for the stall timeout and received none. QUIC acknowledges within its
// acknowledgment delay every packet that asks for it, and an idle connection
// sends a keep alive that asks, so on a path that works packets always come
// back. A stalled member is unavailable at once, as if its URL test had
// failed, until its next successful test. The check reads counters and sends
// nothing.
//
// One packet without an answer is not a stall. The counter of sent packets
// includes packets that are never answered: the acknowledgment a client sends
// for the last packet of a transfer, or a path MTU probe that is lost. After
// such a packet an idle connection sends nothing until its next keep alive.
// A dead path looks different: what was sent is sent again, at growing
// intervals, so packets keep going out. The member is stalled only when
// packets went out in at least stallMinSends samples since the last one came
// in.

const stallMinSends = 3

const (
	minStallTimeout = time.Second
	// A connection that receives nothing is closed by its own idle timeout
	// (30s by default for QUIC), and a closed connection is not stalled, so
	// a longer stall timeout could never pass.
	maxStallTimeout = 20 * time.Second
)

func checkStallTimeout(timeout time.Duration) error {
	if timeout != 0 && (timeout < minStallTimeout || timeout > maxStallTimeout) {
		return E.New("stall_timeout must be between ", minStallTimeout, " and ", maxStallTimeout)
	}
	return nil
}

// stallState follows one transport connection of a member between samples.
type stallState struct {
	sent     uint64
	received uint64
	// waitingSince is when packets were first seen going out with none
	// received since; zero when the last change was a received packet.
	waitingSince time.Time
	// sends is the number of samples since then in which packets went out.
	sends int
}

// stallDetector decides from samples of a member's transport statistics
// whether the member is stalled.
type stallDetector struct {
	timeout time.Duration
	states  map[string][]stallState
}

func newStallDetector(timeout time.Duration) *stallDetector {
	return &stallDetector{timeout: timeout, states: make(map[string][]stallState)}
}

// sample takes the statistics of the open connections of a member at now and
// reports whether the member is stalled: it has open connections, and every
// one of them has been waiting for a packet for the timeout.
func (d *stallDetector) sample(member string, stats []adapter.TransportStats, now time.Time) bool {
	states := d.states[member]
	if len(states) != len(stats) {
		// a connection opened or closed: which state belonged to which
		// connection is not known any more, so start again
		states = make([]stallState, len(stats))
		for i, it := range stats {
			states[i] = stallState{sent: it.PacketsSent, received: it.PacketsReceived}
		}
		d.states[member] = states
		return false
	}
	stalled := len(stats) > 0
	for i, it := range stats {
		state := &states[i]
		switch {
		case it.PacketsSent < state.sent || it.PacketsReceived < state.received:
			// the counters went back: another connection took this place
			state.waitingSince, state.sends = time.Time{}, 0
		case it.PacketsReceived > state.received:
			state.waitingSince, state.sends = time.Time{}, 0
		case it.PacketsSent > state.sent:
			if state.waitingSince.IsZero() {
				state.waitingSince = now
			}
			state.sends++
		}
		state.sent = it.PacketsSent
		state.received = it.PacketsReceived
		if state.waitingSince.IsZero() || state.sends < stallMinSends || now.Sub(state.waitingSince) < d.timeout {
			stalled = false
		}
	}
	return stalled
}

// reset forgets what a member was waiting for, after it has been reported.
func (d *stallDetector) reset(member string) {
	states := d.states[member]
	for i := range states {
		states[i].waitingSince, states[i].sends = time.Time{}, 0
	}
}

// watchStalls samples the members until the group closes. A member that
// stalls loses its test history, and update makes the group select again.
func (g *URLTestGroup) watchStalls(timeout time.Duration, update func()) {
	var members []adapter.OutboundWithTransportStats
	for _, detour := range g.outbounds {
		if member, hasStats := detour.(adapter.OutboundWithTransportStats); hasStats {
			members = append(members, member)
		}
	}
	if len(members) == 0 {
		return
	}
	detector := newStallDetector(timeout)
	ticker := time.NewTicker(max(timeout/4, 250*time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-g.close:
			return
		case now := <-ticker.C:
			var stalled bool
			for _, member := range members {
				if !detector.sample(member.Tag(), member.TransportStats(), now) {
					continue
				}
				detector.reset(member.Tag())
				if g.markStalled(member, timeout) {
					stalled = true
				}
			}
			if stalled {
				update()
			}
		}
	}
}

// markStalled drops the test history of a stalled member and reports whether
// it had any: a member that is unavailable already is not reported again.
func (g *URLTestGroup) markStalled(member adapter.Outbound, timeout time.Duration) bool {
	var marked bool
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		if !common.Contains(member.Network(), network) {
			continue
		}
		realTag := RealTag(member, network)
		if realTag == "" || g.history.LoadURLTestHistory(realTag) == nil {
			continue
		}
		g.history.DeleteURLTestHistory(realTag)
		marked = true
	}
	if marked {
		g.logger.Warn("outbound ", member.Tag(), " sent packets for ", timeout, " and received none, marked unavailable")
	}
	return marked
}
