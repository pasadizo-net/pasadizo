package route

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/bufio"

	"github.com/stretchr/testify/require"
)

func newFirstByteTest(t *testing.T, timeout time.Duration) (watched net.Conn, remote net.Conn, timeouts *atomic.Int32) {
	local, remote := net.Pipe()
	timeouts = new(atomic.Int32)
	watched = newFirstByteConn(local, timeout, func() { timeouts.Add(1) })
	t.Cleanup(func() {
		watched.Close()
		remote.Close()
	})
	return
}

func TestFirstByteTimeoutWithoutAnswer(t *testing.T) {
	t.Parallel()
	watched, remote, timeouts := newFirstByteTest(t, 50*time.Millisecond)
	go io.Copy(io.Discard, remote)
	_, err := watched.Write([]byte("hello"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return timeouts.Load() == 1 }, time.Second, 5*time.Millisecond)
	// reported once, also when more is written
	_, err = watched.Write([]byte("again"))
	require.NoError(t, err)
	time.Sleep(120 * time.Millisecond)
	require.EqualValues(t, 1, timeouts.Load())
}

func TestFirstByteAnswerInTime(t *testing.T) {
	t.Parallel()
	watched, remote, timeouts := newFirstByteTest(t, 80*time.Millisecond)
	go func() {
		buffer := make([]byte, 5)
		_, _ = io.ReadFull(remote, buffer)
		_, _ = remote.Write([]byte("x"))
	}()
	_, err := watched.Write([]byte("hello"))
	require.NoError(t, err)
	buffer := make([]byte, 1)
	_, err = io.ReadFull(watched, buffer)
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, timeouts.Load())
}

func TestFirstByteNoTimeoutBeforeWrite(t *testing.T) {
	t.Parallel()
	// a protocol where the server speaks first, or a client that has not
	// sent anything yet: nothing was asked, so nothing is missing
	_, _, timeouts := newFirstByteTest(t, 30*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	require.Zero(t, timeouts.Load())
}

func TestFirstByteNoTimeoutAfterClose(t *testing.T) {
	t.Parallel()
	watched, remote, timeouts := newFirstByteTest(t, 60*time.Millisecond)
	go io.Copy(io.Discard, remote)
	_, err := watched.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, watched.Close())
	time.Sleep(150 * time.Millisecond)
	require.Zero(t, timeouts.Load())
}

func TestFirstByteCountsThroughCopy(t *testing.T) {
	t.Parallel()
	// the copy loops unwrap the connection; the answer must still be seen
	watched, remote, timeouts := newFirstByteTest(t, 100*time.Millisecond)
	go func() {
		buffer := make([]byte, 5)
		_, _ = io.ReadFull(remote, buffer)
		_, _ = remote.Write([]byte("answer"))
		remote.Close()
	}()
	clientSide, client := net.Pipe()
	t.Cleanup(func() { clientSide.Close(); client.Close() })
	go func() { _, _ = bufio.Copy(watched, clientSide) }()
	go func() { _, _ = bufio.Copy(clientSide, watched) }()
	_, err := client.Write([]byte("hello"))
	require.NoError(t, err)
	answer := make([]byte, 6)
	_, err = io.ReadFull(client, answer)
	require.NoError(t, err)
	require.Equal(t, "answer", string(answer))
	time.Sleep(250 * time.Millisecond)
	require.Zero(t, timeouts.Load())
}
