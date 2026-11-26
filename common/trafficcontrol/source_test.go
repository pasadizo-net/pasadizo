package trafficcontrol

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-tun"
	M "github.com/sagernet/sing/common/metadata"
)

type testOutbound struct {
	adapter.Outbound
	outboundType string
}

func (o testOutbound) Type() string {
	return o.outboundType
}

func (o testOutbound) Tag() string {
	return o.outboundType + "-out"
}

func openFlow(manager *Manager, inbound string, source string, outboundType string, upload int, download int) tun.FlowTracker {
	flow := manager.RoutedFlow(context.Background(), adapter.InboundContext{
		Inbound:       inbound,
		InboundType:   C.TypeTun,
		Source:        M.ParseSocksaddrHostPort(source, 1000),
		OutboundChain: []adapter.Outbound{testOutbound{outboundType: outboundType}},
	}, nil, nil)
	flow.AttachFlow(nil)
	flow.CountForward(upload)
	flow.CountReverse(download)
	return flow
}

func TestSourceStatistics(t *testing.T) {
	manager := NewManager()
	openFlow(manager, "tun-a", "192.168.80.2", C.TypeDirect, 10, 100)
	openFlow(manager, "tun-a", "::ffff:192.168.80.2", C.TypeDNS, 1, 2)
	openFlow(manager, "tun-b", "192.168.80.2", C.TypeDirect, 20, 200)
	openFlow(manager, "tun-a", "192.168.80.3", C.TypeDirect, 30, 300).CloseFlow(tun.FlowCloseReason(0))
	// Push more connections than the closed list holds, so that some of them
	// are only kept in the folded totals.
	for range closedConnectionsLimit + 5 {
		openFlow(manager, "tun-a", "192.168.80.4", C.TypeDirect, 1, 2).CloseFlow(tun.FlowCloseReason(0))
	}
	expected := []SourceStatistics{
		{SourceKey: SourceKey{Source: netip.MustParseAddr("192.168.80.2")}, Upload: 31, Download: 302, Connections: 2},
		{SourceKey: SourceKey{Source: netip.MustParseAddr("192.168.80.3")}, Upload: 30, Download: 300},
		{SourceKey: SourceKey{Source: netip.MustParseAddr("192.168.80.4")}, Upload: closedConnectionsLimit + 5, Download: 2 * (closedConnectionsLimit + 5)},
	}
	assertSourceStatistics(t, manager.SourceStatistics(false), expected)
	manager.Clear()
	assertSourceStatistics(t, manager.SourceStatistics(false), expected)

	uploadTotal, downloadTotal := manager.Total()
	var uploadSum, downloadSum int64
	for _, item := range manager.SourceStatistics(false) {
		uploadSum += item.Upload
		downloadSum += item.Download
	}
	if uploadSum != uploadTotal || downloadSum != downloadTotal {
		t.Fatalf("source totals %d/%d differ from manager totals %d/%d", uploadSum, downloadSum, uploadTotal, downloadTotal)
	}

	assertSourceStatistics(t, manager.SourceStatistics(true), []SourceStatistics{
		{SourceKey: SourceKey{Source: netip.MustParseAddr("192.168.80.2"), InboundType: C.TypeTun, Inbound: "tun-a"}, Upload: 11, Download: 102, Connections: 1},
		{SourceKey: SourceKey{Source: netip.MustParseAddr("192.168.80.2"), InboundType: C.TypeTun, Inbound: "tun-b"}, Upload: 20, Download: 200, Connections: 1},
		{SourceKey: SourceKey{Source: netip.MustParseAddr("192.168.80.3"), InboundType: C.TypeTun, Inbound: "tun-a"}, Upload: 30, Download: 300},
		{SourceKey: SourceKey{Source: netip.MustParseAddr("192.168.80.4"), InboundType: C.TypeTun, Inbound: "tun-a"}, Upload: closedConnectionsLimit + 5, Download: 2 * (closedConnectionsLimit + 5)},
	})
}

func assertSourceStatistics(t *testing.T, actual []SourceStatistics, expected []SourceStatistics) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("got %d sources, expected %d: %+v", len(actual), len(expected), actual)
	}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Errorf("source %d: got %+v, expected %+v", i, actual[i], expected[i])
		}
	}
}
