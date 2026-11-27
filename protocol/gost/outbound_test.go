package gost_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	box "github.com/pasadizo-net/pasadizo"
	"github.com/pasadizo-net/pasadizo/adapter"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/include"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
	"github.com/xtaci/smux"
)

// gostServer is a gost v3 "http" handler on an "mtls" (or "mtcp") listener,
// built on xtaci/smux, the library gost uses, with gost's mux defaults.
type gostServer struct {
	t        *testing.T
	listener net.Listener
	auth     string
	sessions atomic.Int32
	access   sync.Mutex
	conns    []net.Conn
}

func newGostServer(t *testing.T, useTLS bool, username, password string) *gostServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	if useTLS {
		listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{selfSignedCertificate(t)}})
	}
	server := &gostServer{t: t, listener: listener}
	if username != "" {
		server.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	}
	go server.serve()
	t.Cleanup(func() {
		listener.Close()
		server.killSessions()
	})
	return server
}

func (s *gostServer) port() uint16 {
	return uint16(s.listener.Addr().(*net.TCPAddr).Port)
}

func (s *gostServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.access.Lock()
		s.conns = append(s.conns, conn)
		s.access.Unlock()
		s.sessions.Add(1)
		go s.serveSession(conn)
	}
}

func (s *gostServer) serveSession(conn net.Conn) {
	config := smux.DefaultConfig()
	config.Version = 1
	session, err := smux.Server(conn, config)
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

func (s *gostServer) serveStream(stream net.Conn) {
	defer stream.Close()
	reader := bufio.NewReader(stream)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	if request.Method != http.MethodConnect {
		io.WriteString(stream, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	if s.auth != "" && request.Header.Get("Proxy-Authorization") != s.auth {
		// gost with probeResist code:404
		io.WriteString(stream, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n")
		return
	}
	target, err := net.DialTimeout("tcp", request.Host, 5*time.Second)
	if err != nil {
		io.WriteString(stream, "HTTP/1.1 503 Service Unavailable\r\n\r\n")
		return
	}
	defer target.Close()
	io.WriteString(stream, "HTTP/1.1 200 Connection established\r\nProxy-Agent: gost/3.0\r\n\r\n")
	go func() {
		io.Copy(target, reader)
		target.(*net.TCPConn).CloseWrite()
	}()
	io.Copy(stream, target)
}

// killSessions drops every session's conn without closing smux cleanly, the
// way a dead path would.
func (s *gostServer) killSessions() {
	s.access.Lock()
	defer s.access.Unlock()
	for _, conn := range s.conns {
		conn.Close()
	}
	s.conns = nil
}

func selfSignedCertificate(t *testing.T) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "caffex.ir", Organization: []string{"IT"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func echoServer(t *testing.T) M.Socksaddr {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()
	return M.SocksaddrFromNet(listener.Addr())
}

func gostOutbound(tag string, port uint16, useTLS bool, username, password, detour string) option.Outbound {
	options := &option.GostOutboundOptions{
		DialerOptions: option.DialerOptions{Detour: detour},
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
		Username:      username,
		Password:      password,
		Mux: &option.GostMuxOptions{
			KeepAliveInterval: badoption.Duration(time.Second),
			KeepAliveTimeout:  badoption.Duration(3 * time.Second),
			OpenTimeout:       badoption.Duration(3 * time.Second),
		},
	}
	if useTLS {
		options.TLS = &option.OutboundTLSOptions{Enabled: true, ServerName: "srv3.caffex.ir", Insecure: true}
	}
	return option.Outbound{Type: C.TypeGost, Tag: tag, Options: options}
}

func startBox(t *testing.T, outbounds ...option.Outbound) *box.Box {
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			Log:       &option.LogOptions{Level: "warning"},
			Outbounds: outbounds,
		},
	})
	require.NoError(t, err)
	require.NoError(t, instance.Start())
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})
	return instance
}

func dial(t *testing.T, instance *box.Box, tag string, destination M.Socksaddr) (net.Conn, error) {
	outbound, loaded := instance.Outbound().Outbound(tag)
	require.True(t, loaded)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return outbound.DialContext(ctx, N.NetworkTCP, destination)
}

func requireEcho(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	message := strings.Repeat("pasadizo", 8192)
	go io.WriteString(conn, message)
	buffer := make([]byte, len(message))
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := io.ReadFull(conn, buffer)
	require.NoError(t, err)
	require.Equal(t, message, string(buffer))
}

func TestGostRoundTrip(t *testing.T) {
	echo := echoServer(t)
	for _, useTLS := range []bool{true, false} {
		name := "mtcp"
		if useTLS {
			name = "mtls"
		}
		t.Run(name, func(t *testing.T) {
			server := newGostServer(t, useTLS, "user", "pass")
			instance := startBox(t, gostOutbound("gost", server.port(), useTLS, "user", "pass", ""))
			for range 3 {
				conn, err := dial(t, instance, "gost", echo)
				require.NoError(t, err)
				requireEcho(t, conn)
			}
			// Streams share one session.
			require.EqualValues(t, 1, server.sessions.Load())
		})
	}
}

func TestGostWrongAuth(t *testing.T) {
	echo := echoServer(t)
	server := newGostServer(t, true, "user", "pass")
	instance := startBox(t, gostOutbound("gost", server.port(), true, "user", "wrong", ""))
	_, err := dial(t, instance, "gost", echo)
	require.ErrorContains(t, err, "404")
	require.False(t, adapter.IsDestinationError(err))
}

func TestGostRelayCannotReachExit(t *testing.T) {
	// The relay answers 503 for the exit: through the detour, that is the
	// exit outbound's server being unreachable, not a destination error.
	relay := newGostServer(t, true, "", "")
	instance := startBox(t,
		gostOutbound("relay", relay.port(), true, "", "", ""),
		gostOutbound("exit", 1, true, "", "", "relay"),
	)
	_, err := dial(t, instance, "exit", echoServer(t))
	require.Error(t, err)
	require.False(t, adapter.IsDestinationError(err))
}

func TestGostUnreachableTarget(t *testing.T) {
	server := newGostServer(t, true, "", "")
	instance := startBox(t, gostOutbound("gost", server.port(), true, "", "", ""))
	_, err := dial(t, instance, "gost", M.ParseSocksaddr("127.0.0.1:1"))
	require.ErrorContains(t, err, "503")
	require.True(t, adapter.IsDestinationError(err))
	// A refused CONNECT does not cost the session.
	conn, err := dial(t, instance, "gost", echoServer(t))
	require.NoError(t, err)
	requireEcho(t, conn)
	require.EqualValues(t, 1, server.sessions.Load())
}

func TestGostSessionRecreated(t *testing.T) {
	echo := echoServer(t)
	server := newGostServer(t, true, "", "")
	instance := startBox(t, gostOutbound("gost", server.port(), true, "", "", ""))
	conn, err := dial(t, instance, "gost", echo)
	require.NoError(t, err)
	requireEcho(t, conn)

	server.killSessions()
	// The next dial either sees the session closed or fails to open a
	// stream on it; either way it must succeed on a new session.
	require.Eventually(t, func() bool {
		conn, err := dial(t, instance, "gost", echo)
		if err != nil {
			return false
		}
		requireEcho(t, conn)
		return true
	}, 10*time.Second, 100*time.Millisecond)
	require.EqualValues(t, 2, server.sessions.Load())
}

func TestGostDeadServerFailsFast(t *testing.T) {
	// A listener that accepts and then never answers: TLS never completes.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	instance := startBox(t, gostOutbound("gost", uint16(listener.Addr().(*net.TCPAddr).Port), true, "", "", ""))
	start := time.Now()
	_, err = dial(t, instance, "gost", echoServer(t))
	require.Error(t, err)
	require.Less(t, time.Since(start), 6*time.Second)
}

func TestGostDetourChain(t *testing.T) {
	echo := echoServer(t)
	relay := newGostServer(t, true, "relay", "secret")
	exit := newGostServer(t, true, "exit", "secret")
	instance := startBox(t,
		gostOutbound("relay", relay.port(), true, "relay", "secret", ""),
		gostOutbound("exit", exit.port(), true, "exit", "secret", "relay"),
	)
	for range 3 {
		conn, err := dial(t, instance, "exit", echo)
		require.NoError(t, err)
		requireEcho(t, conn)
	}
	require.EqualValues(t, 1, relay.sessions.Load())
	require.EqualValues(t, 1, exit.sessions.Load())
}

func TestGostConcurrentDials(t *testing.T) {
	echo := echoServer(t)
	server := newGostServer(t, true, "", "")
	instance := startBox(t, gostOutbound("gost", server.port(), true, "", "", ""))
	var group sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		group.Go(func() {
			conn, err := dial(t, instance, "gost", echo)
			if err != nil {
				errs <- err
				return
			}
			conn.Close()
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, server.sessions.Load())
}
