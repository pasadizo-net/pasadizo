package group

import (
	"context"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

func transportStats(sent uint64, received uint64) adapter.TransportStats {
	return adapter.TransportStats{PacketsSent: sent, PacketsReceived: received}
}

func TestStallDetector(t *testing.T) {
	t.Parallel()
	start := time.Now()
	at := func(seconds float64) time.Time { return start.Add(time.Duration(seconds * float64(time.Second))) }
	detector := newStallDetector(3 * time.Second)

	// traffic both ways is never a stall, however long it lasts
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(10, 10)}, at(0)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(50, 40)}, at(2)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(90, 80)}, at(4)))
	// an idle connection is not a stall either: nothing was asked
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(90, 80)}, at(60)))

	// packets go out and none come back: stalled once that lasted the timeout
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(95, 80)}, at(61)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(99, 80)}, at(63)))
	require.True(t, detector.sample("a", []adapter.TransportStats{transportStats(104, 80)}, at(64)))

	// one packet back ends it
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(110, 81)}, at(65)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(120, 81)}, at(66)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(130, 81)}, at(68)))
	require.True(t, detector.sample("a", []adapter.TransportStats{transportStats(140, 81)}, at(69.5)))

	// after it was reported, it has to stall for the whole timeout again
	detector.reset("a")
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(150, 81)}, at(70)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(160, 81)}, at(72)))
	require.True(t, detector.sample("a", []adapter.TransportStats{transportStats(170, 81)}, at(73)))
}

func TestStallDetectorNewConnection(t *testing.T) {
	t.Parallel()
	start := time.Now()
	detector := newStallDetector(2 * time.Second)
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(100, 100)}, start))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(110, 100)}, start.Add(time.Second)))
	// the connection was replaced: the counters start again, and so does the wait
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(3, 0)}, start.Add(3*time.Second)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(5, 0)}, start.Add(4*time.Second)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(6, 0)}, start.Add(5*time.Second)))
	require.True(t, detector.sample("a", []adapter.TransportStats{transportStats(7, 0)}, start.Add(6*time.Second)))
	// no open connection: nothing to judge
	require.False(t, detector.sample("a", nil, start.Add(20*time.Second)))
	require.False(t, detector.sample("a", nil, start.Add(40*time.Second)))
}

func TestStallDetectorConnectionPool(t *testing.T) {
	t.Parallel()
	start := time.Now()
	detector := newStallDetector(2 * time.Second)
	// two connections: the member is stalled only when both are
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(10, 10), transportStats(10, 10)}, start))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(20, 10), transportStats(20, 20)}, start.Add(time.Second)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(30, 10), transportStats(30, 30)}, start.Add(4*time.Second)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(40, 10), transportStats(40, 30)}, start.Add(5*time.Second)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(50, 10), transportStats(50, 30)}, start.Add(6*time.Second)))
	require.True(t, detector.sample("a", []adapter.TransportStats{transportStats(60, 10), transportStats(60, 30)}, start.Add(8*time.Second)))
}

func TestMarkStalled(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 1, "a", "b")
	f.group.logger = logger.NOP()
	f.round("a", "b")
	f.requireSelected("a")
	require.True(t, f.group.markStalled(f.group.outbounds[0], 3*time.Second))
	f.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonHistoryUpdate)
	f.requireSelected("b")
	// it is unavailable already: nothing to report a second time
	require.False(t, f.group.markStalled(f.group.outbounds[0], 3*time.Second))
}

func TestStallTimeoutOption(t *testing.T) {
	t.Parallel()
	_, err := NewLoadBalance(context.Background(), nil, nil, "balance", option.LoadBalanceOutboundOptions{
		Outbounds:    []string{"a", "b"},
		StallTimeout: badoption.Duration(5 * time.Second),
	})
	require.NoError(t, err)
	_, err = NewLoadBalance(context.Background(), nil, nil, "balance", option.LoadBalanceOutboundOptions{
		Outbounds:    []string{"a", "b"},
		StallTimeout: badoption.Duration(100 * time.Millisecond),
	})
	require.ErrorContains(t, err, "stall_timeout")
	_, err = NewLoadBalance(context.Background(), nil, nil, "balance", option.LoadBalanceOutboundOptions{
		Outbounds:    []string{"a", "b"},
		StallTimeout: badoption.Duration(30 * time.Second),
	})
	require.ErrorContains(t, err, "stall_timeout")
	_, err = NewFallback(context.Background(), nil, nil, "fallback", option.FallbackOutboundOptions{
		Outbounds:    []string{"a", "b"},
		StallTimeout: badoption.Duration(100 * time.Millisecond),
	})
	require.ErrorContains(t, err, "stall_timeout")
}

func TestStallDetectorUnansweredPacketOnIdleConnection(t *testing.T) {
	t.Parallel()
	start := time.Now()
	at := func(seconds float64) time.Time { return start.Add(time.Duration(seconds * float64(time.Second))) }
	detector := newStallDetector(3 * time.Second)
	// the last packet of a transfer came in just before a sample, and the
	// client's acknowledgment of it went out just after: one packet that the
	// server never answers, then an idle connection until its keep alive
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(100, 200)}, at(0)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(101, 200)}, at(1)))
	for second := 2.0; second < 10; second++ {
		require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(101, 200)}, at(second)), "second %v", second)
	}
	// the keep alive and its answer
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(102, 201)}, at(10)))

	// a dead path sends again and again
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(103, 201)}, at(20)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(104, 201)}, at(21)))
	require.False(t, detector.sample("a", []adapter.TransportStats{transportStats(104, 201)}, at(22)))
	require.True(t, detector.sample("a", []adapter.TransportStats{transportStats(105, 201)}, at(23.5)))
}
