package group

import (
	"context"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/interrupt"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type fallbackTestOutbound struct {
	outbound.Adapter
}

func (o *fallbackTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, E.New("unused")
}

func (o *fallbackTestOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("unused")
}

type fallbackTest struct {
	t     *testing.T
	group *URLTestGroup
	now   time.Time
}

func newFallbackTest(t *testing.T, recoverAfter uint16, tags ...string) *fallbackTest {
	outbounds := make([]adapter.Outbound, 0, len(tags))
	for _, tag := range tags {
		outbounds = append(outbounds, &fallbackTestOutbound{
			Adapter: outbound.NewAdapter(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
		})
	}
	return &fallbackTest{
		t: t,
		group: &URLTestGroup{
			outbounds:      outbounds,
			history:        urltest.NewHistoryStorage(),
			interruptGroup: interrupt.NewGroup(),
			fallback:       newFallbackStrategy(recoverAfter),
		},
		now: time.Now(),
	}
}

// round simulates one health check of the group in which only the given
// outbounds pass.
func (f *fallbackTest) round(available ...string) {
	f.now = f.now.Add(time.Minute)
	for _, detour := range f.group.outbounds {
		if slices.Contains(available, detour.Tag()) {
			f.group.history.StoreURLTestHistory(detour.Tag(), &adapter.URLTestHistory{Time: f.now, Delay: 100})
		} else {
			f.group.history.DeleteURLTestHistory(detour.Tag())
		}
	}
	f.group.tested.Store(true)
	f.group.fallback.updateStreaks(f.group)
	f.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
}

// fail simulates a connection error outside a health check.
func (f *fallbackTest) fail(tag string) {
	f.group.history.DeleteURLTestHistory(tag)
	f.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonHistoryUpdate)
}

func (f *fallbackTest) requireSelected(tag string) {
	f.t.Helper()
	require.Equal(f.t, tag, f.group.selectedOutboundTCP.Load().Tag(), "tcp")
	require.Equal(f.t, tag, f.group.selectedOutboundUDP.Load().Tag(), "udp")
}

func TestFallbackOrder(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 3, "a", "b", "c")
	f.round("a", "b", "c")
	f.requireSelected("a")

	f = newFallbackTest(t, 3, "a", "b", "c")
	f.round("c", "b")
	f.requireSelected("b")
}

func TestFallbackFailover(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 3, "a", "b", "c")
	f.round("a", "b", "c")
	f.requireSelected("a")
	f.round("b", "c")
	f.requireSelected("b")
	f.fail("b")
	f.requireSelected("c")
}

func TestFallbackAllUnavailable(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 3, "a", "b")
	f.round("b")
	f.requireSelected("b")
	f.round()
	f.requireSelected("a")
	selected, available := f.group.Select(N.NetworkTCP)
	require.Equal(t, "a", selected.Tag())
	require.False(t, available)
}

func TestFallbackRecoverAfter(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 3, "a", "b", "c")
	f.round("c")
	f.requireSelected("c")

	// b passes twice, then fails once: its streak starts over.
	f.round("b", "c")
	f.round("b", "c")
	f.requireSelected("c")
	f.round("c")
	f.round("b", "c")
	f.round("b", "c")
	f.requireSelected("c")
	f.round("b", "c")
	f.requireSelected("b")

	// a connection error resets the streak as well.
	f.round("a", "b", "c")
	f.round("a", "b", "c")
	f.group.history.DeleteURLTestHistory("a")
	f.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	f.round("a", "b", "c")
	f.round("a", "b", "c")
	f.requireSelected("b")
	f.round("a", "b", "c")
	f.requireSelected("a")
}

func TestFallbackFailoverPrefersStable(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 2, "a", "b", "c")
	f.round("b", "c")
	f.round("b", "c")
	f.requireSelected("b")

	// a is back but not yet stable when b fails: c, which passed every
	// check, is preferred over a.
	f.round("a", "b", "c")
	f.fail("b")
	f.requireSelected("c")
	f.round("a", "c")
	f.requireSelected("a")
}

func TestFallbackRecoverImmediately(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 1, "a", "b")
	f.round("b")
	f.requireSelected("b")
	f.round("a", "b")
	f.requireSelected("a")
}

func TestFallbackStaleResultNotCounted(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 2, "a", "b")
	f.round("b")
	f.group.history.StoreURLTestHistory("a", &adapter.URLTestHistory{Time: f.now, Delay: 100})
	for range 3 {
		// no new check result for a
		f.group.fallback.updateStreaks(f.group)
		f.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	}
	f.requireSelected("b")
	f.round("a", "b")
	f.requireSelected("a")
}

func TestNewFallback(t *testing.T) {
	t.Parallel()
	detour, err := NewFallback(context.Background(), nil, nil, "fallback", option.FallbackOutboundOptions{
		Outbounds: []string{"a", "b"},
	})
	require.NoError(t, err)
	require.Equal(t, C.TypeFallback, detour.Type())
	require.Equal(t, uint16(defaultFallbackRecoverAfter), detour.(*URLTest).fallback.recoverAfter)

	_, err = NewFallback(context.Background(), nil, nil, "fallback", option.FallbackOutboundOptions{})
	require.Error(t, err)
	_, err = NewFallback(context.Background(), nil, nil, "fallback", option.FallbackOutboundOptions{
		Outbounds: []string{"a", "a"},
	})
	require.Error(t, err)
}
