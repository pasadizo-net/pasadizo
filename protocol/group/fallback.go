package group

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/metrics"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

const defaultFallbackRecoverAfter = 3

func RegisterFallback(registry *outbound.Registry) {
	outbound.Register[option.FallbackOutboundOptions](registry, C.TypeFallback, NewFallback)
}

func NewFallback(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.FallbackOutboundOptions) (adapter.Outbound, error) {
	recoverAfter := options.RecoverAfter
	if recoverAfter == 0 {
		recoverAfter = defaultFallbackRecoverAfter
	}
	outbound := &URLTest{
		Adapter:                      outbound.NewAdapter(C.TypeFallback, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		metrics:                      metrics.FromContext(ctx),
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		timeout:                      time.Duration(options.Timeout),
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,
		fallback:                     newFallbackStrategy(recoverAfter),
		lazy:                         options.Lazy,
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	for i, tag := range outbound.tags {
		if slices.Contains(outbound.tags[:i], tag) {
			return nil, E.New("duplicate tag: ", tag)
		}
	}
	for _, tag := range options.Lazy {
		if !slices.Contains(outbound.tags, tag) {
			return nil, E.New("lazy outbound is not a member: ", tag)
		}
		if tag == outbound.tags[0] {
			return nil, E.New("the first outbound cannot be lazy: ", tag)
		}
	}
	return outbound, nil
}

// fallbackStrategy selects the first available outbound in configured order.
// An outbound earlier than the selected one only takes over again after it
// has passed recoverAfter consecutive checks, so a flapping link does not
// pull traffic back and forth.
type fallbackStrategy struct {
	recoverAfter uint16
	access       sync.Mutex
	streaks      map[string]fallbackStreak
}

type fallbackStreak struct {
	lastCheck time.Time
	count     uint16
}

func newFallbackStrategy(recoverAfter uint16) *fallbackStrategy {
	return &fallbackStrategy{
		recoverAfter: recoverAfter,
		streaks:      make(map[string]fallbackStreak),
	}
}

// updateStreaks counts every new successful check result since the last call
// and resets the count of every outbound without a result.
func (f *fallbackStrategy) updateStreaks(g *URLTestGroup) {
	f.access.Lock()
	defer f.access.Unlock()
	for _, detour := range g.outbounds {
		for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
			if !common.Contains(detour.Network(), network) {
				continue
			}
			realTag := RealTag(detour, network)
			if realTag == "" {
				continue
			}
			history := g.history.LoadURLTestHistory(realTag)
			if history == nil {
				delete(f.streaks, realTag)
				continue
			}
			streak := f.streaks[realTag]
			if !history.Time.After(streak.lastCheck) {
				continue
			}
			streak.lastCheck = history.Time
			if streak.count < f.recoverAfter {
				streak.count++
			}
			f.streaks[realTag] = streak
		}
	}
}

func (f *fallbackStrategy) selectOutbound(g *URLTestGroup, network string) (adapter.Outbound, bool) {
	var selectedOutbound adapter.Outbound
	switch network {
	case N.NetworkTCP:
		selectedOutbound = g.selectedOutboundTCP.Load()
	case N.NetworkUDP:
		selectedOutbound = g.selectedOutboundUDP.Load()
	}
	f.access.Lock()
	defer f.access.Unlock()
	if !g.tested.Load() {
		// until the first test round has finished, the first outbound is
		// taken as available, so that a start does not run on a later one
		// that happened to pass its test first
		for _, detour := range g.outbounds {
			if common.Contains(detour.Network(), network) {
				return detour, true
			}
		}
		return nil, false
	}
	var firstSupported, firstAvailable adapter.Outbound
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		if firstSupported == nil {
			firstSupported = detour
		}
		if g.isDormant(detour.Tag()) {
			// a lazy member that is not being tested: its history, if any,
			// is from the last time it was needed
			continue
		}
		realTag := RealTag(detour, network)
		if realTag == "" || g.history.LoadURLTestHistory(realTag) == nil {
			delete(f.streaks, realTag)
			continue
		}
		if detour == selectedOutbound || f.streaks[realTag].count >= f.recoverAfter {
			return detour, true
		}
		if firstAvailable == nil {
			firstAvailable = detour
		}
	}
	if firstAvailable != nil {
		return firstAvailable, true
	}
	return firstSupported, false
}
