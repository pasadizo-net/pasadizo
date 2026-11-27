package gost_test

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha1"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/go-gost/relay"
	"github.com/golang/snappy"
	"github.com/stretchr/testify/require"
	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
)

const kcpTestKey = "pasadizo-kcp-key"

// kcpRelayServer is a gost v3 "relay" handler on a "kcp" listener with
// kcp.crypt aes, built on the libraries gost uses. Like gost's relay handler
// without noDelay, it sends its answer only together with the first bytes from
// the destination.
type kcpRelayServer struct {
	t         *testing.T
	listener  *kcp.Listener
	username  string
	password  string
	muxConfig *smux.Config
	sessions  atomic.Int32
}

func newKCPRelayServer(t *testing.T, username, password string) *kcpRelayServer {
	pass, err := pbkdf2.Key(sha1.New, kcpTestKey, []byte("kcp-go"), 4096, 32)
	require.NoError(t, err)
	block, err := kcp.NewAESBlockCrypt(pass)
	require.NoError(t, err)
	listener, err := kcp.ListenWithOptions("127.0.0.1:0", block, 10, 3)
	require.NoError(t, err)
	muxConfig := smux.DefaultConfig()
	muxConfig.Version = 2
	server := &kcpRelayServer{t: t, listener: listener, username: username, password: password, muxConfig: muxConfig}
	go server.serve()
	t.Cleanup(func() { listener.Close() })
	return server
}

func (s *kcpRelayServer) port() uint16 {
	return uint16(s.listener.Addr().(*net.UDPAddr).Port)
}

func (s *kcpRelayServer) serve() {
	for {
		conn, err := s.listener.AcceptKCP()
		if err != nil {
			return
		}
		//nolint:staticcheck // as gost does
		conn.SetStreamMode(true)
		conn.SetWriteDelay(false)
		conn.SetNoDelay(1, 10, 2, 1)
		s.sessions.Add(1)
		go s.serveSession(&snappyStream{Conn: conn, writer: snappy.NewBufferedWriter(conn), reader: snappy.NewReader(conn)})
	}
}

func (s *kcpRelayServer) serveSession(conn net.Conn) {
	session, err := smux.Server(conn, s.muxConfig)
	if err != nil {
		conn.Close()
		return
	}
	defer session.Close()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return
		}
		go s.serveStream(stream)
	}
}

func (s *kcpRelayServer) serveStream(stream net.Conn) {
	defer stream.Close()
	var request relay.Request
	_, err := request.ReadFrom(stream)
	if err != nil {
		return
	}
	var username, password, address string
	for _, feature := range request.Features {
		switch f := feature.(type) {
		case *relay.UserAuthFeature:
			username, password = f.Username, f.Password
		case *relay.AddrFeature:
			address = net.JoinHostPort(f.Host, strconv.Itoa(int(f.Port)))
		}
	}
	response := relay.Response{Version: relay.Version1, Status: relay.StatusOK}
	if s.username != "" && (username != s.username || password != s.password) {
		response.Status = relay.StatusUnauthorized
		response.WriteTo(stream)
		return
	}
	target, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		response.Status = relay.StatusNetworkUnreachable
		response.WriteTo(stream)
		return
	}
	defer target.Close()
	go func() {
		io.Copy(target, stream)
		target.(*net.TCPConn).CloseWrite()
	}()
	// The answer goes out with the first bytes from the destination.
	buffer := make([]byte, 32768)
	n, err := target.Read(buffer)
	if err != nil {
		return
	}
	var first bytes.Buffer
	response.WriteTo(&first)
	first.Write(buffer[:n])
	_, err = stream.Write(first.Bytes())
	if err != nil {
		return
	}
	io.Copy(stream, target)
}

type snappyStream struct {
	net.Conn
	writer *snappy.Writer
	reader *snappy.Reader
}

func (c *snappyStream) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *snappyStream) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	if err != nil {
		return n, err
	}
	return n, c.writer.Flush()
}

func kcpRelayOutbound(tag string, port uint16, username, password string) option.Outbound {
	return option.Outbound{Type: C.TypeGost, Tag: tag, Options: &option.GostOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
		Username:      username,
		Password:      password,
		Transport:     "kcp",
		Connector:     "relay",
		KCP: &option.GostKCPOptions{
			Key:   kcpTestKey,
			Crypt: "aes",
			Mode:  "fast3",
		},
		Mux: &option.GostMuxOptions{
			Version:     2,
			OpenTimeout: badoption.Duration(3 * time.Second),
		},
	}}
}

func TestGostKCPRelayRoundTrip(t *testing.T) {
	echo := echoServer(t)
	server := newKCPRelayServer(t, "raha", "secret")
	instance := startBox(t, kcpRelayOutbound("gost", server.port(), "raha", "secret"))
	for range 3 {
		conn, err := dial(t, instance, "gost", echo)
		require.NoError(t, err)
		requireEcho(t, conn)
	}
	// Streams share one KCP session.
	require.EqualValues(t, 1, server.sessions.Load())
}

func TestGostKCPRelayWrongAuth(t *testing.T) {
	server := newKCPRelayServer(t, "raha", "secret")
	instance := startBox(t, kcpRelayOutbound("gost", server.port(), "raha", "wrong"))
	conn, err := dial(t, instance, "gost", echoServer(t))
	require.NoError(t, err)
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.ErrorContains(t, err, "unauthorized")
}

func TestGostKCPRelayUnreachableTarget(t *testing.T) {
	server := newKCPRelayServer(t, "", "")
	instance := startBox(t, kcpRelayOutbound("gost", server.port(), "", ""))
	conn, err := dial(t, instance, "gost", M.ParseSocksaddr("127.0.0.1:1"))
	require.NoError(t, err)
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.ErrorContains(t, err, "network unreachable")
}
