package gost

import (
	"encoding/binary"
	"net"
	"sync/atomic"

	E "github.com/sagernet/sing/common/exceptions"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// gost's icmp transport (x/internal/util/icmp) carries each datagram in the
// data of an ICMP echo request, and the node answers with echo replies. The
// data starts with a header:
//
//	+-------+-------+----------+--------+---------+
//	| MAGIC | FLAGS | RESERVED | LENGTH | PAYLOAD |
//	+-------+-------+----------+--------+---------+
//	|   4   |   2   |    2     |   2    |   VAR   |
//	+-------+-------+----------+--------+---------+
//
// MAGIC is "GOST". The node sets the ack flag on its replies, which tells
// them apart from the echo replies of the node's kernel. The echo identifier
// names the session on the node.
const (
	icmpMagic     = 0x474F5354
	icmpHeaderLen = 10
	icmpFlagAck   = 1
	icmpBufferLen = 4096
)

type icmpClientConn struct {
	net.PacketConn
	id       int
	sequence atomic.Uint32
}

func newICMPClientConn(conn net.PacketConn, id int) *icmpClientConn {
	return &icmpClientConn{PacketConn: conn, id: id}
}

func (c *icmpClientConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buffer := make([]byte, icmpBufferLen)
	for {
		n, addr, err := c.PacketConn.ReadFrom(buffer)
		if err != nil {
			return 0, addr, err
		}
		message, err := icmp.ParseMessage(1, buffer[:n])
		if err != nil || message.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := message.Body.(*icmp.Echo)
		if !ok || echo.ID != c.id {
			continue
		}
		data := echo.Data
		if len(data) < icmpHeaderLen || binary.BigEndian.Uint32(data) != icmpMagic {
			continue
		}
		if binary.BigEndian.Uint16(data[4:])&icmpFlagAck == 0 {
			continue
		}
		length := int(binary.BigEndian.Uint16(data[8:]))
		if len(data)-icmpHeaderLen < length {
			continue
		}
		n = copy(p, data[icmpHeaderLen:icmpHeaderLen+length])
		if ipAddr, isIP := addr.(*net.IPAddr); isIP {
			addr = &net.UDPAddr{IP: ipAddr.IP, Port: c.id, Zone: ipAddr.Zone}
		}
		return n, addr, nil
	}
}

func (c *icmpClientConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if udpAddr, isUDP := addr.(*net.UDPAddr); isUDP {
		addr = &net.IPAddr{IP: udpAddr.IP, Zone: udpAddr.Zone}
	}
	if len(p) > 0xFFFF-icmpHeaderLen {
		return 0, E.New("icmp: datagram too large")
	}
	data := make([]byte, icmpHeaderLen+len(p))
	binary.BigEndian.PutUint32(data, icmpMagic)
	binary.BigEndian.PutUint16(data[8:], uint16(len(p)))
	copy(data[icmpHeaderLen:], p)
	message := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{
			ID:   c.id,
			Seq:  int(uint16(c.sequence.Add(1))),
			Data: data,
		},
	}
	packet, err := message.Marshal(nil)
	if err != nil {
		return 0, err
	}
	_, err = c.PacketConn.WriteTo(packet, addr)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
