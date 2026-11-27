package metrics

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func TestNilRegistryRecordsNothing(t *testing.T) {
	t.Parallel()
	var registry *Registry
	registry.InboundConnection("in", "tcp")
	registry.InboundError("in", errors.New("boom"))
	registry.ObserveDial("out", time.Now(), nil)
	require.Equal(t, Snapshot{}, registry.Snapshot())
}

func TestInboundCounters(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	registry.InboundConnection("in-b", "tcp")
	registry.InboundConnection("in-a", "udp")
	registry.InboundConnection("in-a", "tcp")
	registry.InboundConnection("in-a", "tcp")
	registry.InboundError("in-a", errors.New("dial failed"))
	// A client that goes away is not an error of the proxy.
	registry.InboundError("in-a", net.ErrClosed)
	registry.InboundError("in-a", context.Canceled)
	registry.InboundError("in-a", nil)

	snapshot := registry.Snapshot()
	require.Equal(t, []InboundSnapshot{
		{Inbound: "in-a", Network: "tcp", Connections: 2},
		{Inbound: "in-a", Network: "udp", Connections: 1},
		{Inbound: "in-b", Network: "tcp", Connections: 1},
	}, snapshot.Inbounds)
	require.Equal(t, []InboundErrorSnapshot{{Inbound: "in-a", Errors: 1}}, snapshot.InboundErrors)
}

func TestDialHistogram(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	now := time.Now()
	registry.ObserveDial("out", now.Add(-10*time.Millisecond), nil)  // <= 0.05
	registry.ObserveDial("out", now.Add(-300*time.Millisecond), nil) // <= 0.5
	registry.ObserveDial("out", now.Add(-3*time.Second), errors.New("timeout"))
	registry.ObserveDial("out", now.Add(-time.Minute), errors.New("timeout")) // +Inf

	snapshot := registry.Snapshot()
	require.Len(t, snapshot.Dials, 1)
	dial := snapshot.Dials[0]
	require.Equal(t, "out", dial.Outbound)
	require.Equal(t, uint64(4), dial.Dials)
	require.Equal(t, uint64(2), dial.Errors)
	require.Equal(t, []uint64{1, 1, 1, 2, 2, 2, 3, 3, 3, 4}, dial.Buckets)
	require.InDelta(t, 63.31, dial.Sum, 0.1)
}

func TestCountersAreMonotonicUnderConcurrency(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 1000 {
				registry.InboundConnection("in", "tcp")
				registry.ObserveDial("out", time.Now(), nil)
			}
		})
	}
	var last uint64
	for range 50 {
		snapshot := registry.Snapshot()
		if len(snapshot.Dials) == 1 {
			dial := snapshot.Dials[0]
			require.GreaterOrEqual(t, dial.Dials, last)
			require.GreaterOrEqual(t, dial.Dials, dial.Buckets[len(dial.Buckets)-1])
			last = dial.Dials
		}
	}
	group.Wait()
	snapshot := registry.Snapshot()
	require.Equal(t, uint64(8000), snapshot.Inbounds[0].Connections)
	require.Equal(t, uint64(8000), snapshot.Dials[0].Dials)
	require.Equal(t, uint64(8000), snapshot.Dials[0].Buckets[len(snapshot.Dials[0].Buckets)-1])
}

type fakeOutbound struct {
	adapter.Outbound
	tag string
	err error
}

func (o *fakeOutbound) Tag() string { return o.tag }

func (o *fakeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if o.err != nil {
		return nil, o.err
	}
	client, server := net.Pipe()
	server.Close()
	return client, nil
}

func TestDialContextRecordsByTag(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	conn, err := registry.DialContext(context.Background(), &fakeOutbound{tag: "good"}, "tcp", M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	conn.Close()
	_, err = registry.DialContext(context.Background(), &fakeOutbound{tag: "bad", err: errors.New("refused")}, "tcp", M.ParseSocksaddr("example.com:443"))
	require.Error(t, err)

	snapshot := registry.Snapshot()
	require.Len(t, snapshot.Dials, 2)
	require.Equal(t, "bad", snapshot.Dials[0].Outbound)
	require.Equal(t, uint64(1), snapshot.Dials[0].Errors)
	require.Equal(t, "good", snapshot.Dials[1].Outbound)
	require.Equal(t, uint64(0), snapshot.Dials[1].Errors)

	// A nil registry still dials.
	var nilRegistry *Registry
	conn, err = nilRegistry.DialContext(context.Background(), &fakeOutbound{tag: "good"}, "tcp", M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	conn.Close()
}
