package group

import (
	"context"
	"maps"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/sagernet/sing/common"
	N "github.com/sagernet/sing/common/network"
)

// A lazy member of a fallback group is tested only while the group needs it:
// when no member before it is available, or while it is the selected one. A
// backup on a metered or slow link (mobile data, a satellite) then costs
// nothing while the members before it work. A lazy member that is not tested
// is dormant: it is never selected, and its health is not reported.

func (g *URLTestGroup) setLazy(tags []string) {
	if len(tags) == 0 {
		return
	}
	g.lazy = make(map[string]bool, len(tags))
	g.dormant = make(map[string]bool, len(tags))
	for _, tag := range tags {
		g.lazy[tag] = true
		g.dormant[tag] = true
	}
}

func (g *URLTestGroup) isDormant(tag string) bool {
	if g.lazy == nil {
		return false
	}
	g.dormantAccess.Lock()
	defer g.dormantAccess.Unlock()
	return g.dormant[tag]
}

func (g *URLTestGroup) setDormant(tag string, dormant bool) {
	g.dormantAccess.Lock()
	defer g.dormantAccess.Unlock()
	g.dormant[tag] = dormant
}

// hasPassedTest reports whether the member has a passed test for a network
// it supports.
func (g *URLTestGroup) hasPassedTest(detour adapter.Outbound) bool {
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		if realTag := RealTag(detour, network); realTag != "" && g.history.LoadURLTestHistory(realTag) != nil {
			return true
		}
	}
	return false
}

// lazyNeeded reports whether the lazy member at index must be tested: it is
// selected, or no member before it is available.
func (g *URLTestGroup) lazyNeeded(index int) bool {
	detour := g.outbounds[index]
	if g.selectedOutboundTCP.Load() == detour || g.selectedOutboundUDP.Load() == detour {
		return true
	}
	for _, earlier := range g.outbounds[:index] {
		if !g.isDormant(earlier.Tag()) && g.hasPassedTest(earlier) {
			return false
		}
	}
	return true
}

// testMembers runs one test round. Without lazy members that is one batch.
// With them, the other members are tested first, then each lazy member in
// order that is needed after the results before it.
func (g *URLTestGroup) testMembers(ctx context.Context, force bool) map[string]uint16 {
	test := g.testOutbounds
	if test == nil {
		test = func(ctx context.Context, outbounds []adapter.Outbound, force bool) map[string]uint16 {
			return urlTestOutbounds(ctx, g.outbound, g.history, g.logger, outbounds, g.link, g.interval, g.timeout, force)
		}
	}
	if g.lazy == nil {
		return test(ctx, g.outbounds, force)
	}
	result := test(ctx, common.Filter(g.outbounds, func(it adapter.Outbound) bool {
		return !g.lazy[it.Tag()]
	}), force)
	for index, detour := range g.outbounds {
		if !g.lazy[detour.Tag()] {
			continue
		}
		if !g.lazyNeeded(index) {
			g.setDormant(detour.Tag(), true)
			continue
		}
		maps.Copy(result, test(ctx, []adapter.Outbound{detour}, force))
		g.setDormant(detour.Tag(), false)
	}
	return result
}

// wakeLazy starts a test round at once when a dormant member became needed
// outside a round, for example after a connection through the last available
// member before it failed. Without it the group would wait for its next round
// on a member that is known to be down.
func (g *URLTestGroup) wakeLazy() {
	if g.lazy == nil || !g.started.Load() || g.closed.Load() {
		return
	}
	var needed bool
	for index, detour := range g.outbounds {
		if g.lazy[detour.Tag()] && g.isDormant(detour.Tag()) && g.lazyNeeded(index) {
			needed = true
			break
		}
	}
	if !needed || !g.waking.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer g.waking.Store(false)
		g.CheckOutbounds(g.ctx, false)
	}()
}
