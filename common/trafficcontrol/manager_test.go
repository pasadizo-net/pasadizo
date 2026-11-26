package trafficcontrol

import (
	"sync/atomic"
	"testing"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/require"
)

type testTracker struct {
	metadata TrackerMetadata
}

func (t *testTracker) Metadata() *TrackerMetadata {
	return &t.metadata
}

func (t *testTracker) Close() error {
	return nil
}

func newTestTracker(outbound string, upload int64, download int64) *testTracker {
	tracker := &testTracker{
		metadata: TrackerMetadata{
			ID:       uuid.Must(uuid.NewV4()),
			Upload:   new(atomic.Int64),
			Download: new(atomic.Int64),
			Outbound: outbound,
		},
	}
	tracker.metadata.Upload.Store(upload)
	tracker.metadata.Download.Store(download)
	return tracker
}

func TestManagerOutboundTotal(t *testing.T) {
	t.Parallel()
	manager := NewManager()
	first := newTestTracker("a", 10, 100)
	second := newTestTracker("a", 20, 200)
	third := newTestTracker("b", 1, 2)
	manager.join(first)
	manager.join(second)
	manager.join(third)
	require.Equal(t, map[string]OutboundStatistics{
		"a": {Upload: 30, Download: 300, Connections: 2},
		"b": {Upload: 1, Download: 2, Connections: 1},
	}, manager.OutboundTotal())

	manager.leave(first)
	manager.leave(first)
	manager.Clear()
	require.Equal(t, map[string]OutboundStatistics{
		"a": {Upload: 30, Download: 300, Connections: 1},
		"b": {Upload: 1, Download: 2, Connections: 1},
	}, manager.OutboundTotal())

	manager.leave(second)
	manager.leave(third)
	require.Equal(t, map[string]OutboundStatistics{
		"a": {Upload: 30, Download: 300},
		"b": {Upload: 1, Download: 2},
	}, manager.OutboundTotal())
}

func TestManagerOutboundTotalAfterEviction(t *testing.T) {
	t.Parallel()
	manager := NewManager()
	for range closedConnectionsLimit + 10 {
		tracker := newTestTracker("a", 1, 2)
		manager.join(tracker)
		manager.leave(tracker)
	}
	require.Equal(t, map[string]OutboundStatistics{
		"a": {Upload: closedConnectionsLimit + 10, Download: 2 * (closedConnectionsLimit + 10)},
	}, manager.OutboundTotal())
}
