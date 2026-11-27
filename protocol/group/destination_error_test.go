package group

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/interrupt"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// errorOutbound fails every dial with err.
type errorOutbound struct {
	outbound.Adapter
	err error
}

func (o *errorOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, o.err
}

func (o *errorOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, o.err
}

// newTestFallbackPair builds fallback [primary, backup] whose primary fails
// every dial with err, and runs its first test round with both healthy.
func newTestFallbackPair(err error) *URLTest {
	primary := &errorOutbound{
		Adapter: outbound.NewAdapter(C.TypeDirect, "primary", []string{N.NetworkTCP, N.NetworkUDP}, nil),
		err:     err,
	}
	backup := newEventTestOutbound("backup")
	history := urltest.NewHistoryStorage()
	fallback := &URLTest{
		ctx:      context.Background(),
		logger:   logger.NOP(),
		fallback: newFallbackStrategy(3),
		group: &URLTestGroup{
			outbounds:      []adapter.Outbound{primary, backup},
			history:        history,
			interruptGroup: interrupt.NewGroup(),
			fallback:       newFallbackStrategy(3),
		},
	}
	fallback.group.listeners = &fallback.listeners
	fallback.group.started.Store(true)
	// A ticker already set makes Touch skip the background test loop.
	fallback.group.ticker = time.NewTicker(time.Hour)
	fallback.group.ticker.Stop()
	storeAll(history, "primary", "backup")
	fallback.group.tested.Store(true)
	fallback.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	return fallback
}

func TestFallbackKeepsMemberOnDestinationError(t *testing.T) {
	t.Parallel()
	destinationErr := adapter.NewDestinationError(errTestDial)
	fallback := newTestFallbackPair(destinationErr)
	require.Equal(t, "primary", fallback.Selected(N.NetworkTCP).Tag())

	_, err := fallback.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("dead.example.com", 443))
	require.ErrorIs(t, err, errTestDial)
	require.True(t, adapter.IsDestinationError(err))
	require.NotNil(t, fallback.group.history.LoadURLTestHistory("primary"))
	require.Equal(t, "primary", fallback.Selected(N.NetworkTCP).Tag())

	_, err = fallback.ListenPacket(context.Background(), M.ParseSocksaddrHostPort("dead.example.com", 443))
	require.Error(t, err)
	require.Equal(t, "primary", fallback.Selected(N.NetworkUDP).Tag())
}

func TestFallbackLeavesMemberOnPathError(t *testing.T) {
	t.Parallel()
	fallback := newTestFallbackPair(errTestDial)
	require.Equal(t, "primary", fallback.Selected(N.NetworkTCP).Tag())

	_, err := fallback.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("a.example.com", 443))
	require.ErrorIs(t, err, errTestDial)
	require.Nil(t, fallback.group.history.LoadURLTestHistory("primary"))
	require.Equal(t, "backup", fallback.Selected(N.NetworkTCP).Tag())
}

func TestLoadBalanceCircuitBreakerIgnoresDestinationErrors(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(2)
	s := newTestBreakerLoadBalance(2, outbounds...)
	markTested(s, outbounds...)
	s.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	member := outbounds[0]
	for _, destination := range testDestinations(10) {
		s.ReportConnectionFailure(N.NetworkTCP, member, destination, adapter.NewDestinationError(errTestDial))
	}
	require.Contains(t, s.candidates(N.NetworkTCP), member)
}

func TestDestinationErrorThroughDetourIsServerError(t *testing.T) {
	t.Parallel()
	err := adapter.NewDestinationError(errTestDial)
	require.True(t, adapter.IsDestinationError(err))
	serverErr := adapter.AsServerError(err)
	require.False(t, adapter.IsDestinationError(serverErr))
	require.Equal(t, err.Error(), serverErr.Error())
	require.Equal(t, errTestDial, adapter.AsServerError(errTestDial))
}
