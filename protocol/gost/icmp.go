//go:build with_quic

package gost

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"net"
	"time"

	aTLS "github.com/pasadizo-net/pasadizo/common/tls"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/quic-go"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"

	"golang.org/x/net/icmp"
)

// The defaults of gost's icmp dialer.
const (
	defaultICMPKeepAlivePeriod = 10 * time.Second
	defaultICMPIdleTimeout     = 90 * time.Second
)

var defaultICMPNextProtos = []string{"h3", "quic/v1"}

func (h *Outbound) icmpSessionDialer(ctx context.Context, options option.GostOutboundOptions) (func(ctx context.Context) (session, error), error) {
	if !h.serverAddr.IsIPv4() {
		return nil, E.New("icmp transport: server must be an IPv4 address")
	}
	var tlsConfig *tls.Config
	if options.TLS != nil && options.TLS.Enabled {
		clientConfig, err := aTLS.NewClientWithOptions(aTLS.ClientOptions{
			Context:       ctx,
			Logger:        h.logger,
			ServerAddress: options.Server,
			Options:       *options.TLS,
		})
		if err != nil {
			return nil, err
		}
		tlsConfig, err = clientConfig.STDConfig()
		if err != nil {
			return nil, err
		}
	} else {
		// A gost node without a certificate presents a self-signed one,
		// and a gost client does not verify it by default.
		tlsConfig = &tls.Config{
			ServerName:         h.serverAddr.AddrString(),
			InsecureSkipVerify: true,
		}
	}
	if len(tlsConfig.NextProtos) == 0 {
		tlsConfig.NextProtos = defaultICMPNextProtos
	}
	icmpOptions := common.PtrValueOrDefault(options.ICMP)
	quicConfig := &quic.Config{
		KeepAlivePeriod:      time.Duration(icmpOptions.KeepAlivePeriod),
		MaxIdleTimeout:       time.Duration(icmpOptions.IdleTimeout),
		HandshakeIdleTimeout: h.openTimeout,
		Versions:             []quic.Version{quic.Version1, quic.Version2},
	}
	if quicConfig.KeepAlivePeriod <= 0 {
		quicConfig.KeepAlivePeriod = defaultICMPKeepAlivePeriod
	}
	if quicConfig.MaxIdleTimeout <= 0 {
		quicConfig.MaxIdleTimeout = defaultICMPIdleTimeout
	}
	return func(ctx context.Context) (session, error) {
		return h.newICMPSession(ctx, tlsConfig, quicConfig)
	}, nil
}

// newICMPSession opens a QUIC connection to the node over ICMP echo, as
// gost's icmp dialer does. The raw ICMP socket needs CAP_NET_RAW.
func (h *Outbound) newICMPSession(ctx context.Context, tlsConfig *tls.Config, quicConfig *quic.Config) (session, error) {
	rawConn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, E.Cause(err, "listen icmp")
	}
	var identifier [2]byte
	rand.Read(identifier[:])
	id := int(binary.BigEndian.Uint16(identifier[:])%0xFFFF) + 1
	packetConn := newICMPClientConn(rawConn, id)
	remoteAddr := &net.UDPAddr{IP: h.serverAddr.Addr.AsSlice(), Port: id}
	quicConn, err := quic.DialEarly(ctx, packetConn, remoteAddr, tlsConfig, quicConfig)
	if err != nil {
		rawConn.Close()
		return nil, err
	}
	return &quicSession{conn: quicConn, packetConn: rawConn}, nil
}

type quicSession struct {
	conn       *quic.Conn
	packetConn net.PacketConn
}

func (s *quicSession) OpenStream() (net.Conn, error) {
	stream, err := s.conn.OpenStream()
	if err != nil {
		return nil, err
	}
	return &quicStreamConn{
		Stream:     stream,
		localAddr:  s.conn.LocalAddr(),
		remoteAddr: s.conn.RemoteAddr(),
	}, nil
}

func (s *quicSession) IsClosed() bool {
	return s.conn.Context().Err() != nil
}

func (s *quicSession) Close() error {
	err := s.conn.CloseWithError(0, "")
	s.packetConn.Close()
	return err
}

type quicStreamConn struct {
	*quic.Stream
	localAddr  net.Addr
	remoteAddr net.Addr
}

func (c *quicStreamConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *quicStreamConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

// Close closes both directions. Stream.Close only closes the sending one, and
// data the node already sent would hold the flow control window of the
// connection.
func (c *quicStreamConn) Close() error {
	c.Stream.CancelRead(0)
	return c.Stream.Close()
}
