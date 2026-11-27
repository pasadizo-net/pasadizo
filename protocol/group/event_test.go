package group

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/interrupt"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/experimental/groupwebhook"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

type eventTestOutbound struct {
	outbound.Adapter
}

func newEventTestOutbound(tag string) *eventTestOutbound {
	return &eventTestOutbound{
		Adapter: outbound.NewAdapter(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
	}
}

func (o *eventTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, os.ErrInvalid
}

func (o *eventTestOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

type testEventListener struct {
	access sync.Mutex
	events []adapter.OutboundGroupEvent
}

func (l *testEventListener) NotifyOutboundGroupEvent(event adapter.OutboundGroupEvent) {
	l.access.Lock()
	defer l.access.Unlock()
	event.Time = time.Time{}
	l.events = append(l.events, event)
}

func (l *testEventListener) take() []adapter.OutboundGroupEvent {
	l.access.Lock()
	defer l.access.Unlock()
	events := l.events
	l.events = nil
	return events
}

func newTestURLTestGroup(t *testing.T, listener adapter.OutboundGroupEventListener, tags ...string) (*URLTestGroup, *urltest.HistoryStorage) {
	t.Helper()
	history := urltest.NewHistoryStorage()
	ctx := service.ContextWithPtr(context.Background(), history)
	ctx = service.ContextWith[adapter.OutboundGroupEventListener](ctx, listener)
	var outbounds []adapter.Outbound
	for _, tag := range tags {
		outbounds = append(outbounds, newEventTestOutbound(tag))
	}
	group, err := NewURLTestGroup(ctx, nil, logger.NOP(), "auto", outbounds, "", 0, 0, 0, false)
	require.NoError(t, err)
	return group, history
}

func storeDelay(history *urltest.HistoryStorage, tag string, delay uint16) {
	history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: delay})
}

func TestURLTestGroupEvents(t *testing.T) {
	t.Parallel()
	listener := &testEventListener{}
	group, history := newTestURLTestGroup(t, listener, "a", "b")
	ctx := context.Background()

	storeDelay(history, "a", 100)
	storeDelay(history, "b", 200)
	group.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonURLTest)
	require.Empty(t, listener.take(), "initial selection and healthy members must not be reported")

	history.DeleteURLTestHistory("a")
	group.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonHistoryUpdate)
	require.Equal(t, []adapter.OutboundGroupEvent{
		{
			Type:   adapter.OutboundGroupEventSelected,
			Group:  "auto",
			From:   "a",
			To:     "b",
			Reason: adapter.OutboundGroupEventReasonHistoryUpdate,
		},
		{
			Type:     adapter.OutboundGroupEventHealth,
			Group:    "auto",
			Outbound: "a",
			From:     adapter.OutboundGroupHealthAvailable,
			To:       adapter.OutboundGroupHealthUnavailable,
			Reason:   adapter.OutboundGroupEventReasonHistoryUpdate,
		},
	}, listener.take())

	group.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonURLTest)
	require.Empty(t, listener.take(), "unchanged state must not be reported again")

	storeDelay(history, "a", 50)
	group.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonURLTest)
	require.Equal(t, []adapter.OutboundGroupEvent{
		{
			Type:   adapter.OutboundGroupEventSelected,
			Group:  "auto",
			From:   "b",
			To:     "a",
			Reason: adapter.OutboundGroupEventReasonURLTest,
		},
		{
			Type:     adapter.OutboundGroupEventHealth,
			Group:    "auto",
			Outbound: "a",
			From:     adapter.OutboundGroupHealthUnavailable,
			To:       adapter.OutboundGroupHealthAvailable,
			Delay:    50,
			Reason:   adapter.OutboundGroupEventReasonURLTest,
		},
	}, listener.take())
}

func TestURLTestGroupHealthWaitsForFirstTest(t *testing.T) {
	t.Parallel()
	listener := &testEventListener{}
	group, history := newTestURLTestGroup(t, listener, "a", "b")

	group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonHistoryUpdate)
	require.Empty(t, listener.take(), "untested members must not be reported unavailable")

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	group.performUpdateCheck(canceledCtx, adapter.OutboundGroupEventReasonURLTest)
	require.Empty(t, listener.take(), "a canceled test must not report members")

	storeDelay(history, "b", 100)
	group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	require.Equal(t, []adapter.OutboundGroupEvent{
		{
			Type:   adapter.OutboundGroupEventSelected,
			Group:  "auto",
			From:   "a",
			To:     "b",
			Reason: adapter.OutboundGroupEventReasonURLTest,
		},
		{
			Type:     adapter.OutboundGroupEventHealth,
			Group:    "auto",
			Outbound: "a",
			From:     adapter.OutboundGroupHealthAvailable,
			To:       adapter.OutboundGroupHealthUnavailable,
			Reason:   adapter.OutboundGroupEventReasonURLTest,
		},
	}, listener.take())
}

func newTestSelector(listener adapter.OutboundGroupEventListener, tags ...string) *Selector {
	selector := &Selector{
		Adapter:        outbound.NewAdapter(C.TypeSelector, "select", nil, tags),
		ctx:            context.Background(),
		logger:         logger.NOP(),
		tags:           tags,
		outbounds:      make(map[string]adapter.Outbound),
		interruptGroup: interrupt.NewGroup(),
		eventListener:  listener,
	}
	for _, tag := range tags {
		selector.outbounds[tag] = newEventTestOutbound(tag)
	}
	selector.selected.Store(selector.outbounds[tags[0]])
	return selector
}

func TestSelectorEvents(t *testing.T) {
	t.Parallel()
	listener := &testEventListener{}
	selector := newTestSelector(listener, "a", "b")

	require.True(t, selector.SelectOutbound("a"))
	require.False(t, selector.SelectOutbound("c"))
	require.Empty(t, listener.take())

	require.True(t, selector.SelectOutbound("b"))
	require.Equal(t, []adapter.OutboundGroupEvent{{
		Type:   adapter.OutboundGroupEventSelected,
		Group:  "select",
		From:   "a",
		To:     "b",
		Reason: adapter.OutboundGroupEventReasonSelect,
	}}, listener.take())
}

type testHTTPTransport struct {
	*http.Transport
}

func (t *testHTTPTransport) Reset() {
	t.CloseIdleConnections()
}

type testHTTPClientManager struct {
	transport adapter.HTTPTransport
}

func (m *testHTTPClientManager) ResolveTransport(context.Context, logger.ContextLogger, option.HTTPClientOptions) (adapter.HTTPTransport, error) {
	return m.transport, nil
}

func (m *testHTTPClientManager) DefaultTransport() adapter.HTTPTransport {
	return m.transport
}

func (m *testHTTPClientManager) ResetNetwork() {}

func receiveBody(t *testing.T, received <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case body := <-received:
		return body
	case <-time.After(5 * time.Second):
		t.Fatal("webhook not called")
		return nil
	}
}

func TestURLTestGroupWebhook(t *testing.T) {
	t.Parallel()
	received := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		delete(body, "time")
		received <- body
	}))
	t.Cleanup(server.Close)

	ctx := service.ContextWith[adapter.HTTPClientManager](context.Background(), &testHTTPClientManager{
		transport: &testHTTPTransport{Transport: &http.Transport{}},
	})
	webhook, err := groupwebhook.New(ctx, logger.NOP(), option.GroupWebhookOptions{URL: server.URL})
	require.NoError(t, err)
	scope := adapter.NewScope(ctx, logger.NOP())
	require.NoError(t, webhook.Start(adapter.StartStatePostStart, scope))
	t.Cleanup(func() {
		require.NoError(t, scope.Close())
	})

	group, history := newTestURLTestGroup(t, webhook, "a", "b")
	storeDelay(history, "a", 100)
	storeDelay(history, "b", 200)
	group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	history.DeleteURLTestHistory("a")
	group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonHistoryUpdate)

	require.Equal(t, map[string]any{
		"type":   "selected",
		"group":  "auto",
		"from":   "a",
		"to":     "b",
		"reason": "history_update",
	}, receiveBody(t, received))
	require.Equal(t, map[string]any{
		"type":     "health",
		"group":    "auto",
		"outbound": "a",
		"from":     "available",
		"to":       "unavailable",
		"reason":   "history_update",
	}, receiveBody(t, received))
}

func TestFallbackGroupEvents(t *testing.T) {
	t.Parallel()
	listener := &testEventListener{}
	group, history := newTestURLTestGroup(t, listener, "a", "b")
	group.fallback = newFallbackStrategy(2)
	ctx := context.Background()
	round := func(available ...string) {
		now := time.Now()
		for _, tag := range []string{"a", "b"} {
			if slices.Contains(available, tag) {
				history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: now, Delay: 100})
			} else {
				history.DeleteURLTestHistory(tag)
			}
		}
		group.fallback.updateStreaks(group)
		group.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonURLTest)
		time.Sleep(time.Millisecond)
	}

	round("a", "b")
	require.Empty(t, listener.take())

	round("b")
	require.Equal(t, []adapter.OutboundGroupEvent{
		{
			Type:   adapter.OutboundGroupEventSelected,
			Group:  "auto",
			From:   "a",
			To:     "b",
			Reason: adapter.OutboundGroupEventReasonURLTest,
		},
		{
			Type:     adapter.OutboundGroupEventHealth,
			Group:    "auto",
			Outbound: "a",
			From:     adapter.OutboundGroupHealthAvailable,
			To:       adapter.OutboundGroupHealthUnavailable,
			Reason:   adapter.OutboundGroupEventReasonURLTest,
		},
	}, listener.take())

	round("a", "b")
	require.Equal(t, []adapter.OutboundGroupEvent{
		{
			Type:     adapter.OutboundGroupEventHealth,
			Group:    "auto",
			Outbound: "a",
			From:     adapter.OutboundGroupHealthUnavailable,
			To:       adapter.OutboundGroupHealthAvailable,
			Delay:    100,
			Reason:   adapter.OutboundGroupEventReasonURLTest,
		},
	}, listener.take(), "a recovered member must not be selected before recover_after checks")

	round("a", "b")
	require.Equal(t, []adapter.OutboundGroupEvent{
		{
			Type:   adapter.OutboundGroupEventSelected,
			Group:  "auto",
			From:   "b",
			To:     "a",
			Reason: adapter.OutboundGroupEventReasonURLTest,
		},
	}, listener.take())
}

func TestLoadBalanceGroupEvents(t *testing.T) {
	t.Parallel()
	listener := &testEventListener{}
	group, history := newTestURLTestGroup(t, listener, "a", "b")
	s := &LoadBalance{strategy: C.LoadBalanceStrategyRoundRobin, group: group}
	group.updateCheck = s.performUpdateCheck
	ctx := context.Background()

	storeDelay(history, "a", 100)
	storeDelay(history, "b", 200)
	group.tested.Store(true)
	s.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonURLTest)
	require.Empty(t, listener.take())

	history.DeleteURLTestHistory("b")
	s.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonHistoryUpdate)
	require.Equal(t, []adapter.OutboundGroupEvent{
		{
			Type:     adapter.OutboundGroupEventHealth,
			Group:    "auto",
			Outbound: "b",
			From:     adapter.OutboundGroupHealthAvailable,
			To:       adapter.OutboundGroupHealthUnavailable,
			Reason:   adapter.OutboundGroupEventReasonHistoryUpdate,
		},
	}, listener.take(), "a load balance group reports health only, never a selection")
}
