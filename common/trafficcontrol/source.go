package trafficcontrol

import (
	"cmp"
	"net/netip"
	"slices"

	C "github.com/pasadizo-net/pasadizo/constant"

	"github.com/gofrs/uuid/v5"
)

type SourceKey struct {
	Source      netip.Addr
	InboundType string
	Inbound     string
}

type SourceStatistics struct {
	SourceKey
	Upload      int64
	Download    int64
	Connections int
}

type sourceTraffic struct {
	upload   int64
	download int64
}

func newSourceKey(metadata *TrackerMetadata) SourceKey {
	return SourceKey{
		Source:      metadata.Metadata.Source.Addr.Unmap(),
		InboundType: metadata.Metadata.InboundType,
		Inbound:     metadata.Metadata.Inbound,
	}
}

// foldClosed adds the traffic of a closed connection that leaves the closed
// connections list to the running totals. It must be called with
// closedConnectionsAccess held.
func (m *Manager) foldClosed(metadata *TrackerMetadata) {
	upload := metadata.Upload.Load()
	download := metadata.Download.Load()
	m.closedUploadTotal += upload
	m.closedDownloadTotal += download
	if m.closedSourceTraffic == nil {
		m.closedSourceTraffic = make(map[SourceKey]sourceTraffic)
	}
	key := newSourceKey(metadata)
	traffic := m.closedSourceTraffic[key]
	traffic.upload += upload
	traffic.download += download
	m.closedSourceTraffic[key] = traffic
}

// SourceStatistics returns the total traffic and the number of active
// connections of each source address, the same totals that Total reports
// split by client. With byInbound, a source is reported once for every
// inbound it came through. DNS connections count towards the traffic but not
// towards the active connections, as in the connections list.
func (m *Manager) SourceStatistics(byInbound bool) []SourceStatistics {
	statistics := make(map[SourceKey]*SourceStatistics)
	entry := func(key SourceKey) *SourceStatistics {
		if !byInbound {
			key.InboundType = ""
			key.Inbound = ""
		}
		item := statistics[key]
		if item == nil {
			item = &SourceStatistics{SourceKey: key}
			statistics[key] = item
		}
		return item
	}
	m.closedConnectionsAccess.Lock()
	for key, traffic := range m.closedSourceTraffic {
		item := entry(key)
		item.Upload += traffic.upload
		item.Download += traffic.download
	}
	for element := m.closedConnections.Front(); element != nil; element = element.Next() {
		item := entry(newSourceKey(&element.Value))
		item.Upload += element.Value.Upload.Load()
		item.Download += element.Value.Download.Load()
	}
	m.connections.Range(func(_ uuid.UUID, tracker Tracker) bool {
		metadata := tracker.Metadata()
		item := entry(newSourceKey(metadata))
		item.Upload += metadata.Upload.Load()
		item.Download += metadata.Download.Load()
		if metadata.OutboundType != C.TypeDNS {
			item.Connections++
		}
		return true
	})
	m.closedConnectionsAccess.Unlock()
	result := make([]SourceStatistics, 0, len(statistics))
	for _, item := range statistics {
		result = append(result, *item)
	}
	slices.SortFunc(result, func(a, b SourceStatistics) int {
		return cmp.Or(
			a.Source.Compare(b.Source),
			cmp.Compare(a.InboundType, b.InboundType),
			cmp.Compare(a.Inbound, b.Inbound),
		)
	})
	return result
}
