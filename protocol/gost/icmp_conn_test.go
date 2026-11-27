package gost

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// echoNode answers each echo request the way a gost icmp node and the node's
// kernel both do: the kernel echoes the data back unchanged, and gost answers
// with the ack flag set. A reply for another session is also delivered.
type echoNode struct {
	net.PacketConn
	replies [][]byte
}

func (n *echoNode) WriteTo(packet []byte, addr net.Addr) (int, error) {
	message, err := icmp.ParseMessage(1, packet)
	if err != nil {
		return 0, err
	}
	echo := message.Body.(*icmp.Echo)
	reply := func(id int, data []byte) []byte {
		b, _ := (&icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: &icmp.Echo{ID: id, Seq: echo.Seq, Data: data}}).Marshal(nil)
		return b
	}
	acked := append([]byte(nil), echo.Data...)
	binary.BigEndian.PutUint16(acked[4:], icmpFlagAck)
	n.replies = append(n.replies, reply(echo.ID, echo.Data), reply(echo.ID+1, acked), reply(echo.ID, acked))
	return len(packet), nil
}

func (n *echoNode) ReadFrom(p []byte) (int, net.Addr, error) {
	reply := n.replies[0]
	n.replies = n.replies[1:]
	return copy(p, reply), &net.IPAddr{IP: net.IPv4(192, 0, 2, 1)}, nil
}

func TestICMPClientConn(t *testing.T) {
	node := &echoNode{}
	conn := newICMPClientConn(node, 4242)
	payload := []byte("quic datagram")
	n, err := conn.WriteTo(payload, &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 4242})
	require.NoError(t, err)
	require.Equal(t, len(payload), n)

	buffer := make([]byte, 1500)
	n, addr, err := conn.ReadFrom(buffer)
	require.NoError(t, err)
	// The kernel's echo and the reply for another session are skipped.
	require.Empty(t, node.replies)
	require.Equal(t, payload, buffer[:n])
	require.Equal(t, &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 4242}, addr)
}
