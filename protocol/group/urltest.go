package group

import (
	"context"
	"io"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/interrupt"
	"github.com/pasadizo-net/pasadizo/common/metrics"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

func RegisterURLTest(registry *outbound.Registry) {
	outbound.Register[option.URLTestOutboundOptions](registry, C.TypeURLTest, NewURLTest)
}

var (
	_ adapter.OutboundGroup           = (*URLTest)(nil)
	_ adapter.InterfaceUpdateListener = (*URLTest)(nil)
	_ adapter.Referrer                = (*URLTest)(nil)
)

type URLTest struct {
	outbound.Adapter
	ctx                          context.Context
	metrics                      *metrics.Registry
	outbound                     adapter.OutboundManager
	logger                       log.ContextLogger
	tags                         []string
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	timeout                      time.Duration
	idleTimeout                  time.Duration
	group                        *URLTestGroup
	checkAccess                  sync.Mutex
	interruptExternalConnections bool
	fallback                     *fallbackStrategy
	listeners                    healthListeners
}

func NewURLTest(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.URLTestOutboundOptions) (adapter.Outbound, error) {
	outbound := &URLTest{
		Adapter:                      outbound.NewAdapter(C.TypeURLTest, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		metrics:                      metrics.FromContext(ctx),
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		tolerance:                    options.Tolerance,
		timeout:                      time.Duration(options.Timeout),
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	for i, tag := range outbound.tags {
		if slices.Contains(outbound.tags[:i], tag) {
			return nil, E.New("duplicate tag: ", tag)
		}
	}
	return outbound, nil
}

func (s *URLTest) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	switch stage {
	case adapter.StartStateStart:
		outbounds := make([]adapter.Outbound, 0, len(s.tags))
		for i, tag := range s.tags {
			detour, loaded := s.outbound.Outbound(tag)
			if !loaded {
				return E.New("outbound ", i, " not found: ", tag)
			}
			outbounds = append(outbounds, detour)
		}
		group, err := NewURLTestGroup(s.ctx, s.outbound, s.logger, s.Tag(), outbounds, s.link, s.interval, s.tolerance, s.timeout, s.idleTimeout, s.interruptExternalConnections)
		if err != nil {
			return err
		}
		group.fallback = s.fallback
		group.listeners = &s.listeners
		s.group = group
		if s.fallback != nil {
			// a fallback group selects again as soon as a member group's
			// health changes, instead of at its next test round
			for _, detour := range outbounds {
				if source, isSource := detour.(healthSource); isSource {
					source.addHealthListener(s.memberHealthChanged)
				}
			}
		}
	case adapter.StartStateStarted:
		s.group.PostStart()
		scope.Add(s.group.Close)
	}
	return nil
}

func (s *URLTest) addHealthListener(listener func()) {
	s.listeners.add(listener)
}

func (s *URLTest) memberHealthChanged() {
	group := s.group
	if group == nil || !group.started.Load() || group.closed.Load() {
		return
	}
	s.PerformUpdateCheck()
}

func (s *URLTest) All() []string {
	return s.tags
}

func (s *URLTest) Selected(network string) adapter.Outbound {
	var outbound adapter.Outbound
	if network == N.NetworkUDP {
		outbound = s.group.selectedOutboundUDP.Load()
	} else {
		outbound = s.group.selectedOutboundTCP.Load()
	}
	if outbound == nil {
		outbound, _ = s.group.Select(network)
	}
	return outbound
}

func (s *URLTest) AttachConnection(closer io.Closer) func() {
	s.group.Touch()
	return s.group.interruptGroup.Add(closer, true)
}

func (s *URLTest) References() ([]string, []string) {
	group := s.group
	if group == nil {
		return nil, nil
	}
	var references []string
	selectedOutboundTCP := group.selectedOutboundTCP.Load()
	selectedOutboundUDP := group.selectedOutboundUDP.Load()
	if selectedOutboundTCP != nil {
		references = append(references, selectedOutboundTCP.Tag())
	}
	if selectedOutboundUDP != nil && selectedOutboundUDP != selectedOutboundTCP {
		references = append(references, selectedOutboundUDP.Tag())
	}
	return references, nil
}

func (s *URLTest) URLTest(ctx context.Context) (map[string]uint16, error) {
	return s.group.URLTest(ctx)
}

func (s *URLTest) CheckOutbounds() {
	s.group.CheckOutbounds(s.ctx, true)
}

func (s *URLTest) PerformUpdateCheck() {
	if s.group.performUpdateCheck(s.ctx, adapter.OutboundGroupEventReasonHistoryUpdate) {
		s.group.history.NotifyUpdated()
	}
}

func (s *URLTest) InterfaceUpdated(ctx context.Context) {
	group := s.group
	if group == nil {
		return
	}
	if group.pause.IsDevicePaused() || group.pause.IsNetworkPaused() {
		return
	}
	go func() {
		s.checkAccess.Lock()
		defer s.checkAccess.Unlock()
		if ctx.Err() != nil {
			return
		}
		group.CheckOutbounds(ctx, true)
	}()
}

func (s *URLTest) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	var outbound adapter.Outbound
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		outbound = s.group.selectedOutboundTCP.Load()
	case N.NetworkUDP:
		outbound = s.group.selectedOutboundUDP.Load()
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
	if outbound == nil {
		outbound, _ = s.group.Select(network)
	}
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := s.metrics.DialContext(ctx, outbound, network, destination)
	if err == nil {
		return s.group.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	if !E.IsClosedOrCanceled(err) && !adapter.IsDestinationError(err) {
		s.group.history.DeleteURLTestHistory(RealTag(outbound, N.NetworkName(network)))
		s.PerformUpdateCheck()
	}
	return nil, err
}

func (s *URLTest) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()
	outbound := s.group.selectedOutboundUDP.Load()
	if outbound == nil {
		outbound, _ = s.group.Select(N.NetworkUDP)
	}
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := s.metrics.ListenPacket(ctx, outbound, destination)
	if err == nil {
		return s.group.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	if !E.IsClosedOrCanceled(err) && !adapter.IsDestinationError(err) {
		s.group.history.DeleteURLTestHistory(RealTag(outbound, N.NetworkUDP))
		s.PerformUpdateCheck()
	}
	return nil, err
}

type URLTestGroup struct {
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	pause                        pause.Manager
	pauseCallback                *list.Element[pause.Callback]
	logger                       log.Logger
	tag                          string
	outbounds                    []adapter.Outbound
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	timeout                      time.Duration
	idleTimeout                  time.Duration
	history                      *urltest.HistoryStorage
	checkAccess                  sync.Mutex
	checkDone                    chan struct{}
	tested                       atomic.Bool
	updateCheck                  func(ctx context.Context, reason string) bool
	selectedOutboundTCP          common.TypedValue[adapter.Outbound]
	selectedOutboundUDP          common.TypedValue[adapter.Outbound]
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	access                       sync.Mutex
	updateAccess                 sync.Mutex
	ticker                       *time.Ticker
	close                        chan struct{}
	started                      atomic.Bool
	closed                       atomic.Bool
	lastActive                   common.TypedValue[time.Time]
	fallback                     *fallbackStrategy
	eventListener                adapter.OutboundGroupEventListener
	memberAvailable              map[string]bool
	listeners                    *healthListeners
	healthState                  string
}

func NewURLTestGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, tag string, outbounds []adapter.Outbound, link string, interval time.Duration, tolerance uint16, timeout time.Duration, idleTimeout time.Duration, interruptExternalConnections bool) (*URLTestGroup, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	if tolerance == 0 {
		tolerance = 50
	}
	if timeout == 0 {
		timeout = C.TCPTimeout
	}
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	if history == nil {
		return nil, E.New("missing URL test history storage")
	}
	return &URLTestGroup{
		ctx:                          ctx,
		outbound:                     outboundManager,
		logger:                       logger,
		tag:                          tag,
		outbounds:                    outbounds,
		link:                         link,
		interval:                     interval,
		tolerance:                    tolerance,
		timeout:                      timeout,
		idleTimeout:                  idleTimeout,
		history:                      history,
		close:                        make(chan struct{}),
		pause:                        service.FromContext[pause.Manager](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
		eventListener:                service.FromContext[adapter.OutboundGroupEventListener](ctx),
	}, nil
}

func (g *URLTestGroup) PostStart() {
	g.access.Lock()
	defer g.access.Unlock()
	g.started.Store(true)
	g.lastActive.Store(time.Now())
	go g.CheckOutbounds(g.ctx, false)
}

func (g *URLTestGroup) Touch() {
	if !g.started.Load() {
		return
	}
	g.access.Lock()
	defer g.access.Unlock()
	if g.closed.Load() {
		return
	}
	if g.ticker != nil {
		g.lastActive.Store(time.Now())
		return
	}
	ticker := time.NewTicker(g.interval)
	g.ticker = ticker
	g.pauseCallback = pause.RegisterTicker(g.pause, ticker, g.interval, nil)
	go g.loopCheck(ticker, g.close)
}

func (g *URLTestGroup) Close() error {
	g.access.Lock()
	defer g.access.Unlock()
	if g.closed.Swap(true) {
		return nil
	}
	if g.ticker != nil {
		g.ticker.Stop()
		g.ticker = nil
		g.pause.UnregisterCallback(g.pauseCallback)
		g.pauseCallback = nil
	}
	close(g.close)
	return nil
}

func (g *URLTestGroup) Select(network string) (adapter.Outbound, bool) {
	if g.fallback != nil {
		return g.fallback.selectOutbound(g, network)
	}
	var minDelay uint16
	var minOutbound adapter.Outbound
	var selectedOutbound adapter.Outbound
	switch network {
	case N.NetworkTCP:
		selectedOutbound = g.selectedOutboundTCP.Load()
	case N.NetworkUDP:
		selectedOutbound = g.selectedOutboundUDP.Load()
	}
	if selectedOutbound != nil {
		if history := g.history.LoadURLTestHistory(RealTag(selectedOutbound, network)); history != nil {
			minOutbound = selectedOutbound
			minDelay = history.Delay
		}
	}
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		history := g.history.LoadURLTestHistory(RealTag(detour, network))
		if history == nil {
			continue
		}
		if minDelay == 0 || minDelay > history.Delay+g.tolerance {
			minDelay = history.Delay
			minOutbound = detour
		}
	}
	if minOutbound == nil {
		for _, detour := range g.outbounds {
			if !common.Contains(detour.Network(), network) {
				continue
			}
			return detour, false
		}
		return nil, false
	}
	return minOutbound, true
}

func (g *URLTestGroup) loopCheck(ticker *time.Ticker, closeChan <-chan struct{}) {
	if time.Since(g.lastActive.Load()) > g.interval {
		g.lastActive.Store(time.Now())
		g.CheckOutbounds(g.ctx, false)
	}
	for {
		select {
		case <-closeChan:
			return
		case <-ticker.C:
		}
		if time.Since(g.lastActive.Load()) > g.idleTimeout {
			g.access.Lock()
			if g.ticker == ticker {
				g.ticker.Stop()
				g.ticker = nil
				g.pause.UnregisterCallback(g.pauseCallback)
				g.pauseCallback = nil
			}
			g.access.Unlock()
			return
		}
		g.CheckOutbounds(g.ctx, false)
	}
}

func (g *URLTestGroup) CheckOutbounds(ctx context.Context, force bool) {
	_, _ = g.urlTest(ctx, force)
}

func (g *URLTestGroup) URLTest(ctx context.Context) (map[string]uint16, error) {
	return g.urlTest(ctx, true)
}

func (g *URLTestGroup) urlTest(ctx context.Context, force bool) (map[string]uint16, error) {
	// A round already running is not started again: the caller waits for
	// it instead, so that a group which tests this one as a member reads the
	// result of that round and not the history from before it.
	g.checkAccess.Lock()
	if running := g.checkDone; running != nil {
		g.checkAccess.Unlock()
		select {
		case <-running:
		case <-ctx.Done():
		}
		return make(map[string]uint16), nil
	}
	done := make(chan struct{})
	g.checkDone = done
	g.checkAccess.Unlock()
	defer func() {
		g.checkAccess.Lock()
		g.checkDone = nil
		g.checkAccess.Unlock()
		close(done)
	}()
	result := urlTestOutbounds(ctx, g.outbound, g.history, g.logger, g.outbounds, g.link, g.interval, g.timeout, force)
	if ctx.Err() == nil {
		g.tested.Store(true)
	}
	if g.fallback != nil {
		g.fallback.updateStreaks(g)
	}
	if g.updateCheck != nil {
		g.updateCheck(ctx, adapter.OutboundGroupEventReasonURLTest)
	} else {
		g.performUpdateCheck(ctx, adapter.OutboundGroupEventReasonURLTest)
	}
	g.history.NotifyUpdated()
	return result, nil
}

type urlTestResult struct {
	delay uint16
	err   error
}

type urlTestBatch struct {
	ctx      context.Context
	outbound adapter.OutboundManager
	history  *urltest.HistoryStorage
	logger   log.Logger
	batch    *batch.Batch[any]
	checked  map[string]bool
	groups   []adapter.OutboundGroup
	access   sync.Mutex
	result   map[string]uint16
	stored   map[string]*adapter.URLTestHistory
	deleted  []string
	timeout  time.Duration
}

func URLTestOutbounds(ctx context.Context, outboundManager adapter.OutboundManager, history *urltest.HistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, force bool) map[string]uint16 {
	result := urlTestOutbounds(ctx, outboundManager, history, logger, outbounds, link, interval, C.TCPTimeout, force)
	history.NotifyUpdated()
	return result
}

func urlTestOutbounds(ctx context.Context, outboundManager adapter.OutboundManager, history *urltest.HistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, timeout time.Duration, force bool) map[string]uint16 {
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	testBatch := &urlTestBatch{
		ctx:      ctx,
		outbound: outboundManager,
		history:  history,
		logger:   logger,
		batch:    b,
		checked:  make(map[string]bool),
		result:   make(map[string]uint16),
		stored:   make(map[string]*adapter.URLTestHistory),
		timeout:  timeout,
	}
	testBatch.test(outbounds, link, interval, force)
	b.Wait()
	history.UpdateURLTestHistory(testBatch.stored, testBatch.deleted)
	for _, outboundGroup := range testBatch.groups {
		groupHistory := history.LoadURLTestHistory(RealTag(outboundGroup, N.NetworkTCP))
		if groupHistory != nil {
			testBatch.result[outboundGroup.Tag()] = groupHistory.Delay
		}
	}
	return testBatch.result
}

func (b *urlTestBatch) test(outbounds []adapter.Outbound, link string, interval time.Duration, force bool) {
	for _, detour := range outbounds {
		tag := detour.Tag()
		if b.checked[tag] {
			continue
		}
		switch nested := detour.(type) {
		case *URLTest:
			b.checked[tag] = true
			b.groups = append(b.groups, nested)
			b.batch.Go(tag, func() (any, error) {
				nestedResult, _ := nested.group.urlTest(b.ctx, force)
				b.access.Lock()
				maps.Copy(b.result, nestedResult)
				b.access.Unlock()
				return nil, nil
			})
		case *LoadBalance:
			b.checked[tag] = true
			b.groups = append(b.groups, nested)
			b.batch.Go(tag, func() (any, error) {
				nestedResult, _ := nested.group.urlTest(b.ctx, force)
				b.access.Lock()
				maps.Copy(b.result, nestedResult)
				b.access.Unlock()
				return nil, nil
			})
		case adapter.OutboundGroup:
			b.checked[tag] = true
			b.groups = append(b.groups, nested)
			b.test(common.FilterNotNil(common.Map(nested.All(), func(it string) adapter.Outbound {
				member, _ := b.outbound.Outbound(it)
				return member
			})), link, interval, force)
		default:
			// A result younger than half the interval is kept, so that
			// groups that share an outbound do not test it twice. The whole
			// interval would skip every other round: the result of the last
			// round is stored when its test ends, which is a little less
			// than one interval before the next round starts.
			history := b.history.LoadURLTestHistory(tag)
			if !force && history != nil && time.Since(history.Time) < interval/2 {
				continue
			}
			b.checked[tag] = true
			b.batch.Go(tag, func() (any, error) {
				testCtx, cancel := context.WithTimeout(b.ctx, b.timeout)
				defer cancel()
				testChan := make(chan urlTestResult, 1)
				go func() {
					delay, testErr := urltest.URLTest(testCtx, link, detour)
					testChan <- urlTestResult{delay, testErr}
				}()
				var testResult urlTestResult
				select {
				case testResult = <-testChan:
				case <-testCtx.Done():
					testResult.err = testCtx.Err()
				}
				if testResult.err != nil {
					if b.ctx.Err() != nil {
						return nil, nil
					}
					b.logger.Debug("outbound ", tag, " unavailable: ", testResult.err)
					b.access.Lock()
					b.deleted = append(b.deleted, tag)
					b.access.Unlock()
				} else {
					b.logger.Debug("outbound ", tag, " available: ", testResult.delay, "ms")
					b.access.Lock()
					b.stored[tag] = &adapter.URLTestHistory{
						Time:  time.Now(),
						Delay: testResult.delay,
					}
					b.result[tag] = testResult.delay
					b.access.Unlock()
				}
				return nil, nil
			})
		}
	}
}

func (g *URLTestGroup) performUpdateCheck(ctx context.Context, reason string) bool {
	selected, healthChanged := g.updateSelected(ctx, reason)
	if healthChanged && g.listeners != nil {
		// the listeners run after updateAccess is released, so that a
		// parent group that selects again cannot wait on this one
		g.listeners.notify()
	}
	return selected
}

// updateSelected selects again and reports whether a member was selected and
// whether the selected members or their health changed.
func (g *URLTestGroup) updateSelected(ctx context.Context, reason string) (bool, bool) {
	g.updateAccess.Lock()
	defer g.updateAccess.Unlock()
	var (
		updated  bool
		selected bool
	)
	selectedOutboundTCP := g.selectedOutboundTCP.Load()
	if outbound, _ := g.Select(N.NetworkTCP); outbound != nil && outbound != selectedOutboundTCP {
		if selectedOutboundTCP != nil {
			updated = true
		}
		g.selectedOutboundTCP.Store(outbound)
		selected = true
	}
	selectedOutboundUDP := g.selectedOutboundUDP.Load()
	if outbound, _ := g.Select(N.NetworkUDP); outbound != nil && outbound != selectedOutboundUDP {
		if selectedOutboundUDP != nil {
			updated = true
		}
		g.selectedOutboundUDP.Store(outbound)
		selected = true
	}
	if updated {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
	if g.eventListener != nil {
		g.notifySelected(reason, selectedOutboundTCP, g.selectedOutboundTCP.Load(), selectedOutboundUDP, g.selectedOutboundUDP.Load())
		g.notifyHealth(ctx, reason)
	}
	healthState := g.selectedHealthState()
	healthChanged := healthState != g.healthState
	g.healthState = healthState
	return selected, healthChanged
}

// selectedHealthState names the selected members and whether each has a
// passed test, which is what a group holding this one as a member sees.
func (g *URLTestGroup) selectedHealthState() string {
	var builder strings.Builder
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		var selected adapter.Outbound
		if network == N.NetworkTCP {
			selected = g.selectedOutboundTCP.Load()
		} else {
			selected = g.selectedOutboundUDP.Load()
		}
		if selected == nil {
			builder.WriteString("-\x00")
			continue
		}
		realTag := RealTag(selected, network)
		builder.WriteString(realTag)
		if realTag != "" && g.history.LoadURLTestHistory(realTag) != nil {
			builder.WriteString("+")
		}
		builder.WriteByte(0)
	}
	return builder.String()
}

func (g *URLTestGroup) notifySelected(reason string, fromTCP, toTCP, fromUDP, toUDP adapter.Outbound) {
	tcpChanged := fromTCP != nil && fromTCP != toTCP
	udpChanged := fromUDP != nil && fromUDP != toUDP
	if tcpChanged && udpChanged && fromTCP == fromUDP && toTCP == toUDP {
		g.notifySelectedNetwork(reason, "", fromTCP, toTCP)
		return
	}
	if tcpChanged {
		g.notifySelectedNetwork(reason, N.NetworkTCP, fromTCP, toTCP)
	}
	if udpChanged {
		g.notifySelectedNetwork(reason, N.NetworkUDP, fromUDP, toUDP)
	}
}

func (g *URLTestGroup) notifySelectedNetwork(reason string, network string, from, to adapter.Outbound) {
	g.eventListener.NotifyOutboundGroupEvent(adapter.OutboundGroupEvent{
		Type:    adapter.OutboundGroupEventSelected,
		Group:   g.tag,
		Network: network,
		From:    from.Tag(),
		To:      to.Tag(),
		Reason:  reason,
		Time:    time.Now(),
	})
}

// notifyHealth reports members whose availability flipped since the last check.
// Members are assumed available until the first URL test of the group completes.
func (g *URLTestGroup) notifyHealth(ctx context.Context, reason string) {
	if ctx.Err() != nil {
		return
	}
	if g.memberAvailable == nil {
		if reason != adapter.OutboundGroupEventReasonURLTest {
			return
		}
		g.memberAvailable = make(map[string]bool, len(g.outbounds))
		for _, detour := range g.outbounds {
			g.memberAvailable[detour.Tag()] = true
		}
	}
	for _, detour := range g.outbounds {
		history := g.history.LoadURLTestHistory(RealTag(detour, N.NetworkTCP))
		available := history != nil
		if g.memberAvailable[detour.Tag()] == available {
			continue
		}
		g.memberAvailable[detour.Tag()] = available
		event := adapter.OutboundGroupEvent{
			Type:     adapter.OutboundGroupEventHealth,
			Group:    g.tag,
			Outbound: detour.Tag(),
			Reason:   reason,
			Time:     time.Now(),
		}
		if available {
			event.From = adapter.OutboundGroupHealthUnavailable
			event.To = adapter.OutboundGroupHealthAvailable
			event.Delay = history.Delay
		} else {
			event.From = adapter.OutboundGroupHealthAvailable
			event.To = adapter.OutboundGroupHealthUnavailable
		}
		g.eventListener.NotifyOutboundGroupEvent(event)
	}
}
