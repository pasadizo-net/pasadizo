package group

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/interrupt"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

var errTestDial = errors.New("test dial failed")

func TestFailureBreaker(t *testing.T) {
	t.Parallel()
	now := time.Now()
	b := newFailureBreaker(3, 10*time.Second)
	a := M.ParseSocksaddrHostPort("a.example.com", 443)
	for range 10 {
		require.False(t, b.fail("m", a, now), "one destination must not trip a member")
	}
	// the same host on another port and in another case is the same destination
	require.False(t, b.fail("m", M.ParseSocksaddrHostPort("A.example.com", 80), now))
	require.False(t, b.fail("m", M.ParseSocksaddrHostPort("b.example.com", 443), now))
	require.False(t, b.fail("other", M.ParseSocksaddrHostPort("c.example.com", 443), now), "failures of another member count apart")
	require.True(t, b.fail("m", M.ParseSocksaddrHostPort("10.0.0.1", 443), now))
	require.True(t, b.isTripped("m"))
	require.False(t, b.isTripped("other"))

	// failures spread wider than the window do not trip
	b = newFailureBreaker(3, 10*time.Second)
	require.False(t, b.fail("m", M.ParseSocksaddrHostPort("a.example.com", 443), now))
	require.False(t, b.fail("m", M.ParseSocksaddrHostPort("b.example.com", 443), now.Add(6*time.Second)))
	require.False(t, b.fail("m", M.ParseSocksaddrHostPort("c.example.com", 443), now.Add(12*time.Second)))
	require.True(t, b.fail("m", M.ParseSocksaddrHostPort("d.example.com", 443), now.Add(13*time.Second)))
}

func newTestBreakerLoadBalance(threshold int, outbounds ...adapter.Outbound) *LoadBalance {
	s := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, outbounds...)
	s.logger = logger.NOP()
	s.breaker = newFailureBreaker(threshold, 10*time.Second)
	s.group.listeners = &s.listeners
	return s
}

func TestLoadBalanceCircuitBreaker(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(4)
	s := newTestBreakerLoadBalance(3, outbounds...)
	markTested(s, outbounds...)
	s.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	member := outbounds[1]

	for range 5 {
		s.ReportConnectionFailure(N.NetworkTCP, member, M.ParseSocksaddrHostPort("dead.example.com", 443), errTestDial)
	}
	require.Contains(t, s.candidates(N.NetworkTCP), member, "one dead destination must not mark a member down")

	s.ReportConnectionFailure(N.NetworkTCP, member, M.ParseSocksaddrHostPort("b.example.com", 443), errTestDial)
	s.ReportConnectionFailure(N.NetworkTCP, member, M.ParseSocksaddrHostPort("c.example.com", 443), errTestDial)
	require.NotContains(t, s.candidates(N.NetworkTCP), member)
	require.Len(t, s.candidates(N.NetworkTCP), 3)

	// its next successful test brings it back
	markTested(s, outbounds...)
	require.Contains(t, s.candidates(N.NetworkTCP), member)

	// a closed or canceled connection is not a failure
	for i := range 5 {
		s.ReportConnectionFailure(N.NetworkTCP, member, M.ParseSocksaddrHostPort("x"+string(rune('a'+i))+".example.com", 443), context.Canceled)
	}
	require.Contains(t, s.candidates(N.NetworkTCP), member)
}

func TestLoadBalanceCircuitBreakerDisabled(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(2)
	s := newTestLoadBalance(C.LoadBalanceStrategyConsistentHashing, outbounds...)
	markTested(s, outbounds...)
	for _, destination := range testDestinations(20) {
		s.ReportConnectionFailure(N.NetworkTCP, outbounds[0], destination, errTestDial)
	}
	require.Len(t, s.candidates(N.NetworkTCP), 2)
}

func TestLoadBalanceCircuitBreakerBeforeFirstRound(t *testing.T) {
	t.Parallel()
	outbounds := newTestOutbounds(3)
	s := newTestBreakerLoadBalance(2, outbounds...)
	require.Len(t, s.candidates(N.NetworkTCP), 3)
	s.ReportConnectionFailure(N.NetworkTCP, outbounds[0], M.ParseSocksaddrHostPort("a.example.com", 443), errTestDial)
	s.ReportConnectionFailure(N.NetworkTCP, outbounds[0], M.ParseSocksaddrHostPort("b.example.com", 443), errTestDial)
	require.Equal(t, outbounds[1:], s.candidates(N.NetworkTCP))
}

func TestLoadBalanceDialFailureFeedsBreaker(t *testing.T) {
	t.Parallel()
	// testOutbound fails every dial with net.ErrClosed, which is not counted
	closed := newTestOutbounds(1)
	s := newTestBreakerLoadBalance(2, closed...)
	markTested(s, closed...)
	for _, destination := range testDestinations(4) {
		_, err := s.DialContext(context.Background(), N.NetworkTCP, destination)
		require.ErrorIs(t, err, net.ErrClosed)
	}
	require.NotNil(t, s.group.history.LoadURLTestHistory(closed[0].Tag()))

	failing := &failingOutbound{testOutbound: testOutbound{tag: "failing", network: []string{N.NetworkTCP, N.NetworkUDP}}}
	s = newTestBreakerLoadBalance(2, failing)
	markTested(s, failing)
	for _, destination := range testDestinations(2) {
		_, err := s.DialContext(context.Background(), N.NetworkTCP, destination)
		require.ErrorIs(t, err, errTestDial)
	}
	require.Nil(t, s.group.history.LoadURLTestHistory("failing"))
}

type failingOutbound struct {
	testOutbound
}

func (o *failingOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, errTestDial
}

// newTestFallbackOverLoadBalance builds fallback [balance [lane-0, lane-1], backup]
// with the wiring Start does.
func newTestFallbackOverLoadBalance(t *testing.T) (*URLTest, *LoadBalance, adapter.Outbound) {
	t.Helper()
	history := urltest.NewHistoryStorage()
	lanes := newTestOutbounds(2)
	balance := &LoadBalance{
		Adapter:  outbound.NewAdapter(C.TypeLoadBalance, "balance", []string{N.NetworkTCP, N.NetworkUDP}, nil),
		logger:   logger.NOP(),
		strategy: C.LoadBalanceStrategyConsistentHashing,
		breaker:  newFailureBreaker(2, 10*time.Second),
		group: &URLTestGroup{
			outbounds:      lanes,
			history:        history,
			interruptGroup: interrupt.NewGroup(),
		},
	}
	balance.group.listeners = &balance.listeners
	balance.group.updateCheck = balance.performUpdateCheck
	backup := newEventTestOutbound("backup")
	fallback := &URLTest{
		ctx:      context.Background(),
		fallback: newFallbackStrategy(3),
		group: &URLTestGroup{
			outbounds:      []adapter.Outbound{balance, backup},
			history:        history,
			interruptGroup: interrupt.NewGroup(),
			fallback:       newFallbackStrategy(3),
		},
	}
	fallback.group.listeners = &fallback.listeners
	fallback.group.started.Store(true)
	balance.addHealthListener(fallback.memberHealthChanged)
	return fallback, balance, backup
}

func storeAll(history *urltest.HistoryStorage, tags ...string) {
	for _, tag := range tags {
		history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	}
}

func TestFallbackFollowsMemberGroupHealth(t *testing.T) {
	t.Parallel()
	fallback, balance, backup := newTestFallbackOverLoadBalance(t)
	history := fallback.group.history
	storeAll(history, "lane-0", "lane-1", "backup")
	balance.group.tested.Store(true)
	balance.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	fallback.group.tested.Store(true)
	fallback.group.fallback.updateStreaks(fallback.group)
	fallback.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	require.Equal(t, "balance", fallback.Selected(N.NetworkTCP).Tag())

	// one lane down: the balance group is still available
	history.DeleteURLTestHistory("lane-0")
	balance.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	require.Equal(t, "balance", fallback.Selected(N.NetworkTCP).Tag())

	// the balance group's own test round finds every lane down: the fallback
	// group moves at once, without a round of its own
	history.DeleteURLTestHistory("lane-1")
	balance.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	require.Equal(t, backup.Tag(), fallback.Selected(N.NetworkTCP).Tag())
	require.Equal(t, backup.Tag(), fallback.Selected(N.NetworkUDP).Tag())
}

func TestFallbackFollowsCircuitBreaker(t *testing.T) {
	t.Parallel()
	fallback, balance, backup := newTestFallbackOverLoadBalance(t)
	history := fallback.group.history
	storeAll(history, "lane-0", "lane-1", "backup")
	balance.group.tested.Store(true)
	balance.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	fallback.group.tested.Store(true)
	fallback.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	require.Equal(t, "balance", fallback.Selected(N.NetworkTCP).Tag())

	for _, lane := range balance.group.outbounds {
		for _, destination := range testDestinations(2) {
			balance.ReportConnectionFailure(N.NetworkTCP, lane, destination, errTestDial)
		}
	}
	require.Equal(t, backup.Tag(), fallback.Selected(N.NetworkTCP).Tag())
}

func TestFallbackFirstBeforeFirstRound(t *testing.T) {
	t.Parallel()
	f := newFallbackTest(t, 3, "a", "b")
	// b answered first while the round is still running
	f.group.history.StoreURLTestHistory("b", &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	selected, available := f.group.Select(N.NetworkTCP)
	require.Equal(t, "a", selected.Tag())
	require.True(t, available)
	f.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonHistoryUpdate)
	f.requireSelected("a")
	f.round("a", "b")
	f.requireSelected("a")
}

func TestURLTestGroupWaitsForRunningRound(t *testing.T) {
	t.Parallel()
	group := &URLTestGroup{
		history:        urltest.NewHistoryStorage(),
		interruptGroup: interrupt.NewGroup(),
		logger:         logger.NOP(),
		timeout:        C.TCPTimeout,
	}
	running := make(chan struct{})
	group.checkDone = running
	returned := make(chan struct{})
	go func() {
		_, _ = group.urlTest(context.Background(), true)
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("urlTest returned while a round was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(running)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("urlTest did not return after the running round finished")
	}
}

type hangingOutbound struct {
	outbound.Adapter
}

func (o *hangingOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (o *hangingOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestURLTestTimeout(t *testing.T) {
	t.Parallel()
	history := urltest.NewHistoryStorage()
	ctx := service.ContextWithPtr(context.Background(), history)
	hanging := &hangingOutbound{Adapter: outbound.NewAdapter(C.TypeDirect, "hanging", []string{N.NetworkTCP}, nil)}
	history.StoreURLTestHistory("hanging", &adapter.URLTestHistory{Time: time.Now(), Delay: 100})
	group, err := NewURLTestGroup(ctx, nil, logger.NOP(), "auto", []adapter.Outbound{hanging}, "", 0, 0, 200*time.Millisecond, 0, false)
	require.NoError(t, err)
	start := time.Now()
	_, _ = group.urlTest(ctx, true)
	require.Less(t, time.Since(start), 2*time.Second)
	require.Nil(t, history.LoadURLTestHistory("hanging"))

	group, err = NewURLTestGroup(ctx, nil, logger.NOP(), "auto", []adapter.Outbound{hanging}, "", 0, 0, 0, 0, false)
	require.NoError(t, err)
	require.Equal(t, C.TCPTimeout, group.timeout, "the timeout defaults to the TCP timeout")
}

func TestGroupTimeoutOptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	urlTestOutbound, err := NewURLTest(ctx, nil, nil, "auto", option.URLTestOutboundOptions{Outbounds: []string{"a"}, Timeout: badoption.Duration(5 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, urlTestOutbound.(*URLTest).timeout)
	fallbackOutbound, err := NewFallback(ctx, nil, nil, "fallback", option.FallbackOutboundOptions{Outbounds: []string{"a"}, Timeout: badoption.Duration(3 * time.Second)})
	require.NoError(t, err)
	require.Equal(t, 3*time.Second, fallbackOutbound.(*URLTest).timeout)
	balanceOutbound, err := NewLoadBalance(ctx, nil, nil, "balance", option.LoadBalanceOutboundOptions{Outbounds: []string{"a"}, Timeout: badoption.Duration(time.Second), FailureThreshold: 3})
	require.NoError(t, err)
	balance := balanceOutbound.(*LoadBalance)
	require.Equal(t, time.Second, balance.timeout)
	require.Equal(t, 3, balance.breaker.threshold)
	require.Equal(t, defaultLoadBalanceFailureWindow, balance.breaker.window)
	balanceOutbound, err = NewLoadBalance(ctx, nil, nil, "balance", option.LoadBalanceOutboundOptions{Outbounds: []string{"a"}})
	require.NoError(t, err)
	require.Nil(t, balanceOutbound.(*LoadBalance).breaker)
	_, err = NewLoadBalance(ctx, nil, nil, "balance", option.LoadBalanceOutboundOptions{Outbounds: []string{"a"}, FailureWindow: badoption.Duration(time.Second)})
	require.Error(t, err)
}
