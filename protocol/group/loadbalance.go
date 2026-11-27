package group

import (
	"context"
	"io"
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
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterLoadBalance(registry *outbound.Registry) {
	outbound.Register[option.LoadBalanceOutboundOptions](registry, C.TypeLoadBalance, NewLoadBalance)
}

var (
	_ adapter.OutboundGroup             = (*LoadBalance)(nil)
	_ adapter.DestinationOutboundGroup  = (*LoadBalance)(nil)
	_ adapter.URLTestGroup              = (*LoadBalance)(nil)
	_ adapter.InterfaceUpdateListener   = (*LoadBalance)(nil)
	_ adapter.Referrer                  = (*LoadBalance)(nil)
	_ adapter.ConnectionFailureReporter = (*LoadBalance)(nil)
)

const defaultLoadBalanceFailureWindow = 10 * time.Second

// LoadBalance spreads connections over its healthy members. Health comes from
// the URL test history that urltest groups use: a member is healthy when its
// last test succeeded. A failed connection does not change it, since it may
// fail for its destination alone, unless the circuit breaker is set: a member
// that fails connections to failureThreshold different destinations within
// failureWindow is unhealthy until its next successful test.
type LoadBalance struct {
	outbound.Adapter
	ctx                          context.Context
	metrics                      *metrics.Registry
	outbound                     adapter.OutboundManager
	logger                       log.ContextLogger
	tags                         []string
	strategy                     string
	link                         string
	interval                     time.Duration
	timeout                      time.Duration
	idleTimeout                  time.Duration
	interruptExternalConnections bool
	group                        *URLTestGroup
	checkAccess                  sync.Mutex
	updateAccess                 sync.Mutex
	healthyTCP                   string
	healthyUDP                   string
	availableTCP                 bool
	availableUDP                 bool
	counter                      atomic.Uint64
	listeners                    healthListeners
	breaker                      *failureBreaker
}

func NewLoadBalance(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.LoadBalanceOutboundOptions) (adapter.Outbound, error) {
	outbound := &LoadBalance{
		Adapter:                      outbound.NewAdapter(C.TypeLoadBalance, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		metrics:                      metrics.FromContext(ctx),
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		strategy:                     options.Strategy,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		timeout:                      time.Duration(options.Timeout),
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if options.FailureThreshold > 0 {
		failureWindow := time.Duration(options.FailureWindow)
		if failureWindow == 0 {
			failureWindow = defaultLoadBalanceFailureWindow
		}
		outbound.breaker = newFailureBreaker(int(options.FailureThreshold), failureWindow)
	} else if options.FailureWindow != 0 {
		return nil, E.New("failure_window needs failure_threshold")
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	for i, tag := range outbound.tags {
		if slices.Contains(outbound.tags[:i], tag) {
			return nil, E.New("duplicate tag: ", tag)
		}
	}
	switch outbound.strategy {
	case "":
		outbound.strategy = C.LoadBalanceStrategyConsistentHashing
	case C.LoadBalanceStrategyConsistentHashing, C.LoadBalanceStrategyRoundRobin:
	default:
		return nil, E.New("unknown load balance strategy: ", outbound.strategy)
	}
	return outbound, nil
}

func (s *LoadBalance) Start(stage adapter.StartStage, scope *adapter.Scope) error {
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
		group, err := NewURLTestGroup(s.ctx, s.outbound, s.logger, s.Tag(), outbounds, s.link, s.interval, 0, s.timeout, s.idleTimeout, s.interruptExternalConnections)
		if err != nil {
			return err
		}
		group.updateCheck = s.performUpdateCheck
		group.listeners = &s.listeners
		s.group = group
	case adapter.StartStateStarted:
		s.group.PostStart()
		scope.Add(s.group.Close)
	}
	return nil
}

func (s *LoadBalance) addHealthListener(listener func()) {
	s.listeners.add(listener)
}

func (s *LoadBalance) All() []string {
	return s.tags
}

// Selected returns the first healthy member for the network. The router uses
// SelectedFor instead; Selected names the group's representative member for
// the clash API (`now`) and for URL test history lookups.
func (s *LoadBalance) Selected(network string) adapter.Outbound {
	if s.group == nil {
		return nil
	}
	candidates := s.candidates(network)
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

func (s *LoadBalance) SelectedFor(network string, destination M.Socksaddr) adapter.Outbound {
	if s.group == nil {
		return nil
	}
	return s.pick(s.candidates(network), destination)
}

func (s *LoadBalance) AttachConnection(closer io.Closer) func() {
	s.group.Touch()
	return s.group.interruptGroup.Add(closer, true)
}

func (s *LoadBalance) References() ([]string, []string) {
	if s.group == nil {
		return nil, nil
	}
	var references []string
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		for _, detour := range s.candidates(network) {
			if !slices.Contains(references, detour.Tag()) {
				references = append(references, detour.Tag())
			}
		}
	}
	return references, nil
}

func (s *LoadBalance) URLTest(ctx context.Context) (map[string]uint16, error) {
	return s.group.URLTest(ctx)
}

func (s *LoadBalance) CheckOutbounds() {
	s.group.CheckOutbounds(s.ctx, true)
}

func (s *LoadBalance) PerformUpdateCheck() {
	if s.performUpdateCheck(s.ctx, adapter.OutboundGroupEventReasonHistoryUpdate) {
		s.group.history.NotifyUpdated()
	}
}

func (s *LoadBalance) InterfaceUpdated(ctx context.Context) {
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

func (s *LoadBalance) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	switch N.NetworkName(network) {
	case N.NetworkTCP, N.NetworkUDP:
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
	outbound := s.pick(s.candidates(N.NetworkName(network)), destination)
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := s.metrics.DialContext(ctx, outbound, network, destination)
	if err == nil {
		return s.group.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	s.ReportConnectionFailure(N.NetworkName(network), outbound, destination, err)
	return nil, err
}

func (s *LoadBalance) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()
	outbound := s.pick(s.candidates(N.NetworkUDP), destination)
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := s.metrics.ListenPacket(ctx, outbound, destination)
	if err == nil {
		return s.group.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	s.ReportConnectionFailure(N.NetworkUDP, outbound, destination, err)
	return nil, err
}

// ReportConnectionFailure feeds the circuit breaker. When the member has now
// failed for enough different destinations, its test history is dropped, so
// that it is unhealthy until its next successful test, and the group selects
// again at once.
func (s *LoadBalance) ReportConnectionFailure(network string, member adapter.Outbound, destination M.Socksaddr, err error) {
	if s.breaker == nil || s.group == nil || E.IsClosedOrCanceled(err) {
		return
	}
	if !s.breaker.fail(member.Tag(), destination, time.Now()) {
		return
	}
	s.logger.Warn("outbound ", member.Tag(), " failed for ", s.breaker.threshold, " destinations in ", s.breaker.window, ", marked unavailable")
	if realTag := RealTag(member, network); realTag != "" {
		s.group.history.DeleteURLTestHistory(realTag)
	}
	s.PerformUpdateCheck()
}

// candidates returns the healthy members that support the network. Before the
// first test round completes every member counts as healthy, and when no
// member is healthy all members that support the network are returned.
func (s *LoadBalance) candidates(network string) []adapter.Outbound {
	supported, healthy := s.members(network)
	if len(healthy) == 0 {
		return supported
	}
	return healthy
}

// members returns the members that support the network and those of them
// that are healthy. Before the first test round completes every member that
// the circuit breaker has not tripped counts as healthy.
func (s *LoadBalance) members(network string) (supported []adapter.Outbound, healthy []adapter.Outbound) {
	tested := s.group.tested.Load()
	for _, detour := range s.group.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		supported = append(supported, detour)
		if s.group.history.LoadURLTestHistory(RealTag(detour, network)) != nil ||
			(!tested && (s.breaker == nil || !s.breaker.isTripped(detour.Tag()))) {
			healthy = append(healthy, detour)
		}
	}
	return
}

func (s *LoadBalance) pick(candidates []adapter.Outbound, destination M.Socksaddr) adapter.Outbound {
	switch len(candidates) {
	case 0:
		return nil
	case 1:
		return candidates[0]
	}
	if s.strategy == C.LoadBalanceStrategyRoundRobin {
		return candidates[(s.counter.Add(1)-1)%uint64(len(candidates))]
	}
	return rendezvous(candidates, destination)
}

// rendezvous picks the member with the highest hash of the destination and the
// member's tag (highest random weight hashing), so that removing a member moves
// only the destinations that member had.
func rendezvous(candidates []adapter.Outbound, destination M.Socksaddr) adapter.Outbound {
	keyHash := destinationHash(destination)
	var (
		selected  adapter.Outbound
		bestScore uint64
	)
	for _, detour := range candidates {
		score := mix64(fnv1a(keyHash, detour.Tag()))
		if selected == nil || score > bestScore {
			selected = detour
			bestScore = score
		}
	}
	return selected
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func destinationHash(destination M.Socksaddr) uint64 {
	hash := uint64(fnvOffset64)
	if destination.IsDomain() {
		hash = fnv1a(hash, strings.ToLower(destination.Fqdn))
	} else if destination.Addr.IsValid() {
		address := destination.Addr.Unmap().As16()
		for _, b := range address {
			hash ^= uint64(b)
			hash *= fnvPrime64
		}
	}
	// separator between the destination and the member tag
	hash ^= 0xff
	hash *= fnvPrime64
	return hash
}

func fnv1a(hash uint64, value string) uint64 {
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= fnvPrime64
	}
	return hash
}

// mix64 is the 64-bit finalizer of MurmurHash3; FNV alone spreads short,
// similar keys poorly.
func mix64(hash uint64) uint64 {
	hash ^= hash >> 33
	hash *= 0xff51afd7ed558ccd
	hash ^= hash >> 33
	hash *= 0xc4ceb9fe1a85ec53
	hash ^= hash >> 33
	return hash
}

func (s *LoadBalance) healthySignature(network string) string {
	var builder strings.Builder
	for _, detour := range s.candidates(network) {
		builder.WriteString(detour.Tag())
		builder.WriteByte(0)
	}
	return builder.String()
}

// performUpdateCheck interrupts the group's connections when the set of
// healthy members changed, since that moves destinations between members.
// A load balance group has no single selected member, so it reports only
// member health changes to the group event listener. The groups that hold
// this one as a member hear when its health changed.
func (s *LoadBalance) performUpdateCheck(ctx context.Context, reason string) bool {
	updated, healthChanged := s.updateHealthy(ctx, reason)
	if healthChanged {
		s.listeners.notify()
	}
	return updated
}

func (s *LoadBalance) hasHealthy(network string) bool {
	_, healthy := s.members(network)
	return len(healthy) > 0
}

func (s *LoadBalance) updateHealthy(ctx context.Context, reason string) (bool, bool) {
	s.updateAccess.Lock()
	defer s.updateAccess.Unlock()
	if s.group.eventListener != nil {
		s.group.notifyHealth(ctx, reason)
	}
	availableTCP := s.hasHealthy(N.NetworkTCP)
	availableUDP := s.hasHealthy(N.NetworkUDP)
	availableChanged := availableTCP != s.availableTCP || availableUDP != s.availableUDP
	s.availableTCP = availableTCP
	s.availableUDP = availableUDP
	healthyTCP := s.healthySignature(N.NetworkTCP)
	healthyUDP := s.healthySignature(N.NetworkUDP)
	if healthyTCP == s.healthyTCP && healthyUDP == s.healthyUDP {
		return false, availableChanged
	}
	initialized := s.healthyTCP != "" || s.healthyUDP != ""
	s.healthyTCP = healthyTCP
	s.healthyUDP = healthyUDP
	if initialized {
		s.group.interruptGroup.Interrupt(s.interruptExternalConnections)
	}
	return true, true
}
