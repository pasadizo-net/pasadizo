package route

import (
	"net"
	"sync"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

// lastDestinationGroup returns the index of the last destination group in an
// outbound chain, or -1.
func lastDestinationGroup(chain []adapter.Outbound) int {
	destinationIndex := -1
	for i, it := range chain {
		if _, isDestinationGroup := it.(adapter.DestinationOutboundGroup); isDestinationGroup {
			destinationIndex = i
		}
	}
	return destinationIndex
}

// watchFirstByte wraps the remote side of a TCP connection that goes through
// a member of a group with a first byte timeout. When data was written to
// the member and no byte came back within the timeout, the group hears of a
// failed connection of that member to the destination. The connection itself
// is left alone: the destination may only be slow, and the group counts
// different destinations before it acts.
func (m *ConnectionManager) watchFirstByte(metadata adapter.InboundContext, remoteConn net.Conn) net.Conn {
	chain := metadata.OutboundChain
	destinationIndex := lastDestinationGroup(chain)
	if destinationIndex < 0 || destinationIndex+1 >= len(chain) {
		return remoteConn
	}
	watcher, isWatcher := chain[destinationIndex].(adapter.FirstByteWatcher)
	if !isWatcher {
		return remoteConn
	}
	timeout := watcher.FirstByteTimeout()
	if timeout <= 0 {
		return remoteConn
	}
	member := chain[destinationIndex+1]
	destination := metadata.Destination
	return newFirstByteConn(remoteConn, timeout, func() {
		watcher.ReportConnectionFailure(N.NetworkTCP, member, destination, E.New("no answer from ", destination, " within ", timeout))
	})
}

// firstByteConn calls onTimeout once when, timeout after the first write, no
// byte has been read and the connection has not been closed. It counts with
// a counter connection, which the copy loops and splice keep counting through
// while they unwrap to the fastest path of the connection below.
type firstByteConn struct {
	net.Conn
	timeout   time.Duration
	onTimeout func()
	access    sync.Mutex
	timer     *time.Timer
	settled   bool
}

func newFirstByteConn(conn net.Conn, timeout time.Duration, onTimeout func()) *firstByteConn {
	c := &firstByteConn{timeout: timeout, onTimeout: onTimeout}
	c.Conn = bufio.NewCounterConn(conn, []N.CountFunc{c.onRead}, []N.CountFunc{c.onWrite})
	return c
}

func (c *firstByteConn) onWrite(n int64) {
	if n <= 0 {
		return
	}
	c.access.Lock()
	defer c.access.Unlock()
	if c.settled || c.timer != nil {
		return
	}
	c.timer = time.AfterFunc(c.timeout, c.expire)
}

func (c *firstByteConn) onRead(n int64) {
	if n > 0 {
		c.settle()
	}
}

func (c *firstByteConn) expire() {
	c.access.Lock()
	if c.settled {
		c.access.Unlock()
		return
	}
	c.settled = true
	c.access.Unlock()
	c.onTimeout()
}

func (c *firstByteConn) settle() {
	c.access.Lock()
	defer c.access.Unlock()
	if c.settled {
		return
	}
	c.settled = true
	if c.timer != nil {
		c.timer.Stop()
	}
}

func (c *firstByteConn) Close() error {
	c.settle()
	return c.Conn.Close()
}

func (c *firstByteConn) ReaderReplaceable() bool {
	return true
}

func (c *firstByteConn) WriterReplaceable() bool {
	return true
}

func (c *firstByteConn) Upstream() any {
	return c.Conn
}
