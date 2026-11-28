package adapter

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/varbin"
)

type URLTestHistory struct {
	Time  time.Time `json:"time"`
	Delay uint16    `json:"delay"`
}

type V2RayServer interface {
	LifecycleService
	StatsService() ConnectionTracker
}

type CacheFile interface {
	LifecycleService

	CacheID() string

	StoreFakeIP() bool
	FakeIPStorage

	StoreRDRC() bool
	RDRCStore

	StoreDNS() bool
	DNSCacheStore

	SetDisableExpire(disableExpire bool)
	SetOptimisticTimeout(timeout time.Duration)
	Flush()

	LoadMode() string
	StoreMode(mode string) error
	LoadSelected(group string) string
	StoreSelected(group string, selected string) error
	LoadGroupExpand(group string) (isExpand bool, loaded bool)
	StoreGroupExpand(group string, expand bool) error
	LoadRuleSet(tag string) *SavedBinary
	SaveRuleSet(tag string, set *SavedBinary) error
}

type SavedBinary struct {
	Content     []byte
	LastUpdated time.Time
	LastEtag    string
	URLHash     []byte
}

func (s *SavedBinary) MarshalBinary() ([]byte, error) {
	var buffer bytes.Buffer
	err := binary.Write(&buffer, binary.BigEndian, uint8(2))
	if err != nil {
		return nil, err
	}
	_, err = varbin.WriteUvarint(&buffer, uint64(len(s.Content)))
	if err != nil {
		return nil, err
	}
	_, err = buffer.Write(s.Content)
	if err != nil {
		return nil, err
	}
	err = binary.Write(&buffer, binary.BigEndian, s.LastUpdated.Unix())
	if err != nil {
		return nil, err
	}
	_, err = varbin.WriteUvarint(&buffer, uint64(len(s.LastEtag)))
	if err != nil {
		return nil, err
	}
	_, err = buffer.WriteString(s.LastEtag)
	if err != nil {
		return nil, err
	}
	_, err = varbin.WriteUvarint(&buffer, uint64(len(s.URLHash)))
	if err != nil {
		return nil, err
	}
	_, err = buffer.Write(s.URLHash)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (s *SavedBinary) UnmarshalBinary(data []byte) error {
	reader := bytes.NewReader(data)
	var version uint8
	err := binary.Read(reader, binary.BigEndian, &version)
	if err != nil {
		return err
	}
	contentLength, err := binary.ReadUvarint(reader)
	if err != nil {
		return err
	}
	if contentLength > uint64(reader.Len()) {
		return E.New("invalid content length: ", contentLength)
	}
	s.Content = make([]byte, contentLength)
	_, err = io.ReadFull(reader, s.Content)
	if err != nil {
		return err
	}
	var lastUpdated int64
	err = binary.Read(reader, binary.BigEndian, &lastUpdated)
	if err != nil {
		return err
	}
	s.LastUpdated = time.Unix(lastUpdated, 0)
	etagLength, err := binary.ReadUvarint(reader)
	if err != nil {
		return err
	}
	if etagLength > uint64(reader.Len()) {
		return E.New("invalid etag length: ", etagLength)
	}
	etagBytes := make([]byte, etagLength)
	_, err = io.ReadFull(reader, etagBytes)
	if err != nil {
		return err
	}
	s.LastEtag = string(etagBytes)
	if version < 2 {
		return nil
	}
	urlHashLength, err := binary.ReadUvarint(reader)
	if err != nil {
		return err
	}
	if urlHashLength > uint64(reader.Len()) {
		return E.New("invalid url hash length: ", urlHashLength)
	}
	s.URLHash = make([]byte, urlHashLength)
	_, err = io.ReadFull(reader, s.URLHash)
	if err != nil {
		return err
	}
	return nil
}

type OutboundGroup interface {
	Outbound
	All() []string
	Selected(network string) Outbound
	AttachConnection(closer io.Closer) (detach func())
}

// DestinationOutboundGroup is an OutboundGroup that picks its member for each
// connection from the connection's destination instead of holding one
// selected member.
type DestinationOutboundGroup interface {
	OutboundGroup
	SelectedFor(network string, destination M.Socksaddr) Outbound
}

// ConnectionFailureReporter is an OutboundGroup that wants to learn which of
// its members failed a connection to which destination, for example to mark
// a member unhealthy when it fails for several destinations.
type ConnectionFailureReporter interface {
	OutboundGroup
	ReportConnectionFailure(network string, member Outbound, destination M.Socksaddr, err error)
}

// TransportStats are the counters of one transport connection that an
// outbound keeps open to its server, for example a QUIC connection.
type TransportStats struct {
	PacketsSent     uint64
	PacketsReceived uint64
	PacketsLost     uint64
	BytesSent       uint64
	BytesReceived   uint64
	SmoothedRTT     time.Duration
}

// OutboundWithTransportStats is an outbound that carries its streams in a few
// long-lived transport connections and reports on each one that is open.
// A group reads it to find a dead path without sending test traffic: packets
// go out and none come back.
type OutboundWithTransportStats interface {
	Outbound
	TransportStats() []TransportStats
}

// FirstByteWatcher is a ConnectionFailureReporter that also counts, as a
// failed connection of a member, a TCP connection that was opened and written
// to but got no byte back within FirstByteTimeout. A proxy protocol that
// opens a stream without a round trip to its server (Hysteria2, TUIC) never
// fails a dial on a dead path, so this is the only failure such a member
// shows between two URL tests. Zero disables it.
type FirstByteWatcher interface {
	ConnectionFailureReporter
	FirstByteTimeout() time.Duration
}

type URLTestGroup interface {
	OutboundGroup
	URLTest(ctx context.Context) (map[string]uint16, error)
	PerformUpdateCheck()
}

const (
	OutboundGroupEventSelected = "selected"
	OutboundGroupEventHealth   = "health"
)

const (
	OutboundGroupEventReasonSelect        = "select"
	OutboundGroupEventReasonURLTest       = "url_test"
	OutboundGroupEventReasonHistoryUpdate = "history_update"
)

const (
	OutboundGroupHealthAvailable   = "available"
	OutboundGroupHealthUnavailable = "unavailable"
)

type OutboundGroupEvent struct {
	Type     string    `json:"type"`
	Group    string    `json:"group"`
	Network  string    `json:"network,omitempty"`
	Outbound string    `json:"outbound,omitempty"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Delay    uint16    `json:"delay,omitempty"`
	Reason   string    `json:"reason"`
	Time     time.Time `json:"time"`
}

// OutboundGroupEventListener receives selection and member health changes of outbound groups.
// Implementations must not block.
type OutboundGroupEventListener interface {
	NotifyOutboundGroupEvent(event OutboundGroupEvent)
}
