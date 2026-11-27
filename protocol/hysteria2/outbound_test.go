package hysteria2

import (
	"context"
	"sync"
	"testing"

	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing-quic/hysteria"
	"github.com/sagernet/sing-quic/hysteria2"

	"github.com/stretchr/testify/require"
)

func newTestOutbound(connections int) *Outbound {
	clients := make([]*hysteria2.Client, connections)
	for i := range clients {
		clients[i] = new(hysteria2.Client)
	}
	return &Outbound{clients: clients}
}

func TestOutboundPickClientSingle(t *testing.T) {
	t.Parallel()
	h := newTestOutbound(1)
	for range 10 {
		require.Same(t, h.clients[0], h.pickClient())
	}
}

func TestOutboundPickClientRoundRobin(t *testing.T) {
	t.Parallel()
	h := newTestOutbound(3)
	for round := range 4 {
		for i := range h.clients {
			require.Same(t, h.clients[i], h.pickClient(), "round %d, client %d", round, i)
		}
	}
}

func TestOutboundPickClientConcurrent(t *testing.T) {
	t.Parallel()
	const (
		connections = 4
		workers     = 8
		picks       = 1000
	)
	h := newTestOutbound(connections)
	var (
		access sync.Mutex
		counts = make(map[*hysteria2.Client]int)
		wg     sync.WaitGroup
	)
	for range workers {
		wg.Go(func() {
			local := make(map[*hysteria2.Client]int)
			for range picks {
				local[h.pickClient()]++
			}
			access.Lock()
			for client, count := range local {
				counts[client] += count
			}
			access.Unlock()
		})
	}
	wg.Wait()
	require.Len(t, counts, connections)
	for _, client := range h.clients {
		require.Equal(t, workers*picks/connections, counts[client])
	}
}

func TestSplitBandwidth(t *testing.T) {
	t.Parallel()
	require.Equal(t, uint64(200*hysteria.MbpsToBps), splitBandwidth(200, 1))
	require.Equal(t, uint64(50*hysteria.MbpsToBps), splitBandwidth(200, 4))
	require.Equal(t, uint64(0), splitBandwidth(0, 4))
}

func TestNewOutboundInvalidConnections(t *testing.T) {
	t.Parallel()
	_, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("test"), "test", option.Hysteria2OutboundOptions{
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
			TLS: &option.OutboundTLSOptions{Enabled: true},
		},
		Connections: -1,
	})
	require.ErrorContains(t, err, "invalid connections")
}
