package group

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type lazyTest struct {
	*fallbackTest
	available []string
	tested    []string
}

func newLazyTest(t *testing.T, recoverAfter uint16, lazy []string, tags ...string) *lazyTest {
	l := &lazyTest{fallbackTest: newFallbackTest(t, recoverAfter, tags...)}
	l.group.setLazy(lazy)
	l.group.testOutbounds = func(ctx context.Context, outbounds []adapter.Outbound, force bool) map[string]uint16 {
		result := make(map[string]uint16)
		for _, detour := range outbounds {
			l.tested = append(l.tested, detour.Tag())
			if slices.Contains(l.available, detour.Tag()) {
				l.group.history.StoreURLTestHistory(detour.Tag(), &adapter.URLTestHistory{Time: l.now, Delay: 100})
				result[detour.Tag()] = 100
			} else {
				l.group.history.DeleteURLTestHistory(detour.Tag())
			}
		}
		return result
	}
	return l
}

// round runs one test round in which only the given outbounds would pass, and
// returns the outbounds it tested.
func (l *lazyTest) round(available ...string) []string {
	l.now = l.now.Add(time.Minute)
	l.available = available
	l.tested = nil
	l.group.testMembers(context.Background(), false)
	l.group.tested.Store(true)
	l.group.fallback.updateStreaks(l.group)
	l.group.performUpdateCheck(context.Background(), adapter.OutboundGroupEventReasonURLTest)
	return l.tested
}

func TestLazyMemberIsNotTestedWhileEarlierWorks(t *testing.T) {
	t.Parallel()
	l := newLazyTest(t, 1, []string{"lte"}, "a", "b", "lte")
	require.Equal(t, []string{"a", "b"}, l.round("a", "b", "lte"))
	l.requireSelected("a")
	// the second member is enough to keep it asleep
	require.Equal(t, []string{"a", "b"}, l.round("b", "lte"))
	l.requireSelected("b")
	require.True(t, l.group.isDormant("lte"))
}

func TestLazyMemberWakesInTheSameRound(t *testing.T) {
	t.Parallel()
	l := newLazyTest(t, 1, []string{"lte"}, "a", "lte")
	l.round("a", "lte")
	l.requireSelected("a")
	require.Equal(t, []string{"a", "lte"}, l.round("lte"))
	l.requireSelected("lte")
	require.False(t, l.group.isDormant("lte"))
}

func TestLazyMemberSleepsAgainAfterRecovery(t *testing.T) {
	t.Parallel()
	l := newLazyTest(t, 2, []string{"lte"}, "a", "lte")
	l.round("lte")
	l.requireSelected("lte")
	// the first member is back but has not passed recover_after tests yet:
	// the group is still on the lazy member, so it is still tested
	require.Equal(t, []string{"a", "lte"}, l.round("a", "lte"))
	l.requireSelected("lte")
	require.Equal(t, []string{"a", "lte"}, l.round("a", "lte"))
	l.requireSelected("a")
	require.Equal(t, []string{"a"}, l.round("a", "lte"))
	require.True(t, l.group.isDormant("lte"))
}

func TestDormantMemberIsNotSelectedOnOldHistory(t *testing.T) {
	t.Parallel()
	l := newLazyTest(t, 1, []string{"lte"}, "a", "lte")
	// a result from the cache file, or from the last time it was needed
	l.group.history.StoreURLTestHistory("lte", &adapter.URLTestHistory{Time: l.now, Delay: 100})
	l.round("a")
	l.requireSelected("a")
	// a connection through the only working member fails between rounds
	l.fail("a")
	l.requireSelected("a")
	require.True(t, l.group.lazyNeeded(1))
}

func TestLazyMembersWakeInOrder(t *testing.T) {
	t.Parallel()
	l := newLazyTest(t, 1, []string{"starlink", "lte"}, "a", "starlink", "lte")
	require.Equal(t, []string{"a", "starlink"}, l.round("starlink", "lte"))
	l.requireSelected("starlink")
	require.True(t, l.group.isDormant("lte"))
	require.Equal(t, []string{"a", "starlink", "lte"}, l.round("lte"))
	l.requireSelected("lte")
}

func TestNewFallbackLazy(t *testing.T) {
	t.Parallel()
	_, err := NewFallback(context.Background(), nil, nil, "fallback", option.FallbackOutboundOptions{
		Outbounds: []string{"a", "b"},
		Lazy:      []string{"c"},
	})
	require.ErrorContains(t, err, "not a member")
	_, err = NewFallback(context.Background(), nil, nil, "fallback", option.FallbackOutboundOptions{
		Outbounds: []string{"a", "b"},
		Lazy:      []string{"a"},
	})
	require.ErrorContains(t, err, "cannot be lazy")
}

func TestLazyMemberCarriesTheNetworkEarlierMembersLack(t *testing.T) {
	t.Parallel()
	l := newLazyTest(t, 1, []string{"lte"}, "naive", "lte")
	// the first member carries TCP only, so UDP has no member before the lazy one
	l.group.outbounds[0] = &fallbackTestOutbound{
		Adapter: outbound.NewAdapter(C.TypeDirect, "naive", []string{N.NetworkTCP}, nil),
	}
	require.Equal(t, []string{"naive", "lte"}, l.round("naive", "lte"))
	require.False(t, l.group.isDormant("lte"))
	require.Equal(t, "naive", l.group.selectedOutboundTCP.Load().Tag())
	require.Equal(t, "lte", l.group.selectedOutboundUDP.Load().Tag())
}

func TestDormantMemberIsNotTheLastResort(t *testing.T) {
	t.Parallel()
	l := newLazyTest(t, 1, []string{"lte"}, "a", "lte")
	l.round("a")
	// the only member that is tested fails between two rounds: the group
	// stays on it until the lazy member has been tested
	l.fail("a")
	selected, available := l.group.Select(N.NetworkUDP)
	require.Equal(t, "a", selected.Tag())
	require.False(t, available)
}
