package group

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/common/interrupt"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type testOutbound struct {
	tag     string
	network []string
}

func (o *testOutbound) Type() string           { return "test" }
func (o *testOutbound) Tag() string            { return o.tag }
func (o *testOutbound) Network() []string      { return o.network }
func (o *testOutbound) Dependencies() []string { return nil }

func (o *testOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (o *testOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func newTestLoadBalance(strategy string, outbounds ...adapter.Outbound) *LoadBalance {
	return &LoadBalance{
		strategy: strategy,
		group: &URLTestGroup{
			outbounds:      outbounds,
			history:        urltest.NewHistoryStorage(),
			interruptGroup: interrupt.NewGroup(),
		},
	}
}

func newTestOutbounds(count int, network ...string) []adapter.Outbound {
	if len(network) == 0 {
		network = []string{N.NetworkTCP, N.NetworkUDP}
	}
	outbounds := make([]adapter.Outbound, count)
	for i := range outbounds {
		outbounds[i] = &testOutbound{tag: "lane-" + strconv.Itoa(i), network: network}
	}
	return outbounds
}

func testDestinations(count int) []M.Socksaddr {
	destinations := make([]M.Socksaddr, 0, count)
	for i := range count {
		if i%2 == 0 {
			destinations = append(destinations, M.ParseSocksaddrHostPort("host-"+strconv.Itoa(i)+".example.com", 443))
		} else {
			destinations = append(destinations, M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}), 443))
		}
	}
	return destinations
}

// markTested records a finished test round in which only the given members
// answered.
func markTested(s *LoadBalance, healthy ...adapter.Outbound) {
	stored := make(map[string]*adapter.URLTestHistory)
	var deleted []string
	for _, detour := range s.group.outbounds {
		if common.Contains(healthy, detour) {
			stored[detour.Tag()] = &adapter.URLTestHistory{Time: time.Now(), Delay: 100}
		} else {
			deleted = append(deleted, detour.Tag())
		}
	}
	s.group.history.UpdateURLTestHistory(stored, deleted)
	s.group.tested.Store(true)
}

func TestLoadBalanceConsistentHashingStable(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(8)
	first := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, outbounds...)
	second := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, newTestOutbounds(8)...)
	for _, destination := range testDestinations(200) {
		selected := first.SelectedFor(N.NetworkTCP, destination)
		require.NotNil(t, selected)
		for range 3 {
			require.Same(t, selected, first.SelectedFor(N.NetworkTCP, destination))
		}
		// same tags in a new group (a restart) give the same member
		require.Equal(t, selected.Tag(), second.SelectedFor(N.NetworkTCP, destination).Tag())
		// the port does not change the member
		other := M.Socksaddr{Addr: destination.Addr, Fqdn: destination.Fqdn, Port: 80}
		require.Same(t, selected, first.SelectedFor(N.NetworkTCP, other))
	}
	upper := M.ParseSocksaddrHostPort("WWW.Example.COM", 443)
	lower := M.ParseSocksaddrHostPort("www.example.com", 443)
	require.Same(t, first.SelectedFor(N.NetworkTCP, lower), first.SelectedFor(N.NetworkTCP, upper))
}

func TestLoadBalanceConsistentHashingSpread(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(8)
	s := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, outbounds...)
	const total = 8000
	counts := make(map[string]int)
	for _, destination := range testDestinations(total) {
		counts[s.SelectedFor(N.NetworkTCP, destination).Tag()]++
	}
	require.Len(t, counts, len(outbounds))
	expected := total / len(outbounds)
	for tag, count := range counts {
		require.InDelta(t, expected, count, float64(expected)*0.2, "member %s got %d of %d", tag, count, total)
	}
}

func TestLoadBalanceConsistentHashingMinimalMove(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(8)
	s := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, outbounds...)
	destinations := testDestinations(4000)
	before := make([]adapter.Outbound, len(destinations))
	for i, destination := range destinations {
		before[i] = s.SelectedFor(N.NetworkTCP, destination)
	}
	down := outbounds[3]
	markTested(s, common.Filter(outbounds, func(it adapter.Outbound) bool { return it != down })...)
	var moved int
	for i, destination := range destinations {
		after := s.SelectedFor(N.NetworkTCP, destination)
		require.NotSame(t, down, after)
		if before[i] == down {
			moved++
		} else {
			require.Same(t, before[i], after, "destination %s moved off a healthy member", destination)
		}
	}
	require.Positive(t, moved)
	// the member comes back and takes exactly its old destinations
	markTested(s, outbounds...)
	for i, destination := range destinations {
		require.Same(t, before[i], s.SelectedFor(N.NetworkTCP, destination))
	}
}

func TestLoadBalanceRoundRobin(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(3)
	s := newTestLoadBalance(C.LoadBalanceStrategyRoundRobin, outbounds...)
	destination := M.ParseSocksaddrHostPort("www.example.com", 443)
	for round := range 3 {
		for i, expected := range outbounds {
			require.Same(t, expected, s.SelectedFor(N.NetworkTCP, destination), "round %d step %d", round, i)
		}
	}
	markTested(s, outbounds[0], outbounds[2])
	seen := make(map[adapter.Outbound]int)
	for range 10 {
		seen[s.SelectedFor(N.NetworkTCP, destination)]++
	}
	require.Len(t, seen, 2)
	require.Equal(t, 5, seen[outbounds[0]])
	require.Equal(t, 5, seen[outbounds[2]])
}

func TestLoadBalanceHealth(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(3)
	s := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, outbounds...)
	// no test round yet: every member is a candidate
	require.Equal(t, outbounds, s.candidates(N.NetworkTCP))
	require.Same(t, outbounds[0], s.Selected(N.NetworkTCP))

	markTested(s, outbounds[1])
	require.Equal(t, []adapter.Outbound{outbounds[1]}, s.candidates(N.NetworkTCP))
	require.Same(t, outbounds[1], s.Selected(N.NetworkTCP))
	for _, destination := range testDestinations(50) {
		require.Same(t, outbounds[1], s.SelectedFor(N.NetworkTCP, destination))
	}

	// nothing healthy: fall back to all members
	markTested(s)
	require.Equal(t, outbounds, s.candidates(N.NetworkTCP))
	counts := make(map[adapter.Outbound]int)
	for _, destination := range testDestinations(300) {
		counts[s.SelectedFor(N.NetworkTCP, destination)]++
	}
	require.Len(t, counts, 3)
}

func TestLoadBalanceNetwork(t *testing.T) {
	t.Parallel()
	tcpOnly := &testOutbound{tag: "tcp-only", network: []string{N.NetworkTCP}}
	udpOnly := &testOutbound{tag: "udp-only", network: []string{N.NetworkUDP}}
	both := &testOutbound{tag: "both", network: []string{N.NetworkTCP, N.NetworkUDP}}
	s := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, tcpOnly, udpOnly, both)
	require.Equal(t, []adapter.Outbound{tcpOnly, both}, s.candidates(N.NetworkTCP))
	require.Equal(t, []adapter.Outbound{udpOnly, both}, s.candidates(N.NetworkUDP))
	for _, destination := range testDestinations(100) {
		require.NotSame(t, udpOnly, s.SelectedFor(N.NetworkTCP, destination))
		require.NotSame(t, tcpOnly, s.SelectedFor(N.NetworkUDP, destination))
	}

	// the fallback when nothing is healthy keeps the network filter
	markTested(s)
	require.Equal(t, []adapter.Outbound{udpOnly, both}, s.candidates(N.NetworkUDP))

	// a group with no member for the network selects nothing
	tcpGroup := newTestLoadBalance(C.LoadBalanceStrategyRoundRobin, tcpOnly)
	require.Nil(t, tcpGroup.SelectedFor(N.NetworkUDP, M.ParseSocksaddrHostPort("www.example.com", 443)))
	require.Nil(t, tcpGroup.Selected(N.NetworkUDP))
}

func TestLoadBalanceUpdateCheck(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(3)
	s := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, outbounds...)
	require.True(t, s.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest))
	require.False(t, s.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest))
	markTested(s, outbounds[0], outbounds[1])
	require.True(t, s.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest))
	require.False(t, s.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest))
}

func TestNewLoadBalanceOptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := NewLoadBalance(ctx, nil, nil, "lb", option.LoadBalanceOutboundOptions{})
	require.ErrorContains(t, err, "missing tags")
	_, err = NewLoadBalance(ctx, nil, nil, "lb", option.LoadBalanceOutboundOptions{Outbounds: []string{"a", "a"}})
	require.ErrorContains(t, err, "duplicate tag")
	_, err = NewLoadBalance(ctx, nil, nil, "lb", option.LoadBalanceOutboundOptions{Outbounds: []string{"a"}, Strategy: "random"})
	require.ErrorContains(t, err, "unknown load balance strategy")
	outbound, err := NewLoadBalance(ctx, nil, nil, "lb", option.LoadBalanceOutboundOptions{Outbounds: []string{"a", "b"}})
	require.NoError(t, err)
	require.Equal(t, C.LoadBalanceStrategyConsistentHashing, outbound.(*LoadBalance).strategy)
	require.Equal(t, C.TypeLoadBalance, outbound.Type())
	require.Equal(t, "LoadBalance", C.ProxyDisplayName(outbound.Type()))
}

func TestNewLoadBalanceFirstByteTimeout(t *testing.T) {
	t.Parallel()
	detour, err := NewLoadBalance(context.Background(), nil, nil, "balance", option.LoadBalanceOutboundOptions{
		Outbounds:        []string{"a", "b"},
		FailureThreshold: 3,
		FirstByteTimeout: badoption.Duration(5 * time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, detour.(adapter.FirstByteWatcher).FirstByteTimeout())

	detour, err = NewLoadBalance(context.Background(), nil, nil, "balance", option.LoadBalanceOutboundOptions{
		Outbounds:        []string{"a", "b"},
		FailureThreshold: 3,
	})
	require.NoError(t, err)
	require.Zero(t, detour.(adapter.FirstByteWatcher).FirstByteTimeout())

	_, err = NewLoadBalance(context.Background(), nil, nil, "balance", option.LoadBalanceOutboundOptions{
		Outbounds:        []string{"a", "b"},
		FirstByteTimeout: badoption.Duration(5 * time.Second),
	})
	require.ErrorContains(t, err, "needs failure_threshold")
}
