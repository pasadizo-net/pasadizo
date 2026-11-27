package route

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	C "github.com/pasadizo-net/pasadizo/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type reportTestOutbound struct {
	outbound.Adapter
}

func newReportTestOutbound(tag string) reportTestOutbound {
	return reportTestOutbound{Adapter: outbound.NewAdapter(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil)}
}

func (o *reportTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (o *reportTestOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

type reportTestGroup struct {
	reportTestOutbound
	updateChecks int
}

func (g *reportTestGroup) All() []string                            { return nil }
func (g *reportTestGroup) Selected(network string) adapter.Outbound { return nil }
func (g *reportTestGroup) AttachConnection(io.Closer) func()        { return func() {} }
func (g *reportTestGroup) URLTest(context.Context) (map[string]uint16, error) {
	return nil, nil
}
func (g *reportTestGroup) PerformUpdateCheck() { g.updateChecks++ }

type reportTestDestinationGroup struct {
	reportTestGroup
	reported []string
}

func (g *reportTestDestinationGroup) SelectedFor(string, M.Socksaddr) adapter.Outbound { return nil }

func (g *reportTestDestinationGroup) ReportConnectionFailure(network string, member adapter.Outbound, destination M.Socksaddr, err error) {
	g.reported = append(g.reported, network+" "+member.Tag()+" "+destination.String())
}

func TestReportConnectionFailure(t *testing.T) {
	t.Parallel()
	errDial := errors.New("dial failed")
	destination := M.ParseSocksaddrHostPort("example.com", 443)
	newManager := func() (*ConnectionManager, *urltest.HistoryStorage) {
		history := urltest.NewHistoryStorage()
		history.StoreURLTestHistory("leaf", &adapter.URLTestHistory{Time: time.Now()})
		return &ConnectionManager{urlTestHistory: history}, history
	}

	// urltest > leaf: the leaf's history is dropped, as upstream does
	manager, history := newManager()
	fallback := &reportTestGroup{reportTestOutbound: newReportTestOutbound("fallback")}
	leaf := newReportTestOutbound("leaf")
	manager.reportConnectionFailure(adapter.InboundContext{Destination: destination, OutboundChain: []adapter.Outbound{fallback, &leaf}}, N.NetworkTCP, errDial)
	require.Nil(t, history.LoadURLTestHistory("leaf"))
	require.Equal(t, 1, fallback.updateChecks)

	// fallback > loadbalance > leaf: the load balance group gets the failure,
	// and the fallback group above it is not told directly
	manager, history = newManager()
	fallback = &reportTestGroup{reportTestOutbound: newReportTestOutbound("fallback")}
	balance := &reportTestDestinationGroup{reportTestGroup: reportTestGroup{reportTestOutbound: newReportTestOutbound("balance")}}
	manager.reportConnectionFailure(adapter.InboundContext{Destination: destination, OutboundChain: []adapter.Outbound{fallback, balance, &leaf}}, N.NetworkUDP, errDial)
	require.NotNil(t, history.LoadURLTestHistory("leaf"), "one failed destination must not mark the member down")
	require.Zero(t, fallback.updateChecks)
	require.Equal(t, []string{"udp leaf example.com:443"}, balance.reported)

	// loadbalance > urltest > leaf: the urltest group below still drops the leaf
	manager, history = newManager()
	inner := &reportTestGroup{reportTestOutbound: newReportTestOutbound("inner")}
	balance = &reportTestDestinationGroup{reportTestGroup: reportTestGroup{reportTestOutbound: newReportTestOutbound("balance")}}
	manager.reportConnectionFailure(adapter.InboundContext{Destination: destination, OutboundChain: []adapter.Outbound{balance, inner, &leaf}}, N.NetworkTCP, errDial)
	require.Nil(t, history.LoadURLTestHistory("leaf"))
	require.Equal(t, 1, inner.updateChecks)
	require.Equal(t, []string{"tcp inner example.com:443"}, balance.reported)

	// a canceled connection is no failure
	manager, history = newManager()
	balance = &reportTestDestinationGroup{reportTestGroup: reportTestGroup{reportTestOutbound: newReportTestOutbound("balance")}}
	manager.reportConnectionFailure(adapter.InboundContext{Destination: destination, OutboundChain: []adapter.Outbound{fallback, balance, &leaf}}, N.NetworkTCP, context.Canceled)
	require.Empty(t, balance.reported)
	require.NotNil(t, history.LoadURLTestHistory("leaf"))
}
