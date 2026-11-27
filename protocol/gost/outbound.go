// Package gost implements an outbound for a gost v3 node: the client side of a
// gost service whose listener is mtls (TLS + smux), mtcp (smux), kcp (KCP +
// smux) or icmp (QUIC over ICMP echo), and whose handler is http or relay.
package gost

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/dialer"
	"github.com/pasadizo-net/pasadizo/common/tls"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/smux"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.GostOutboundOptions](registry, C.TypeGost, NewOutbound)
}

var _ adapter.InterfaceUpdateListener = (*Outbound)(nil)

const (
	// gost's mux defaults to smux protocol version 1.
	defaultMuxVersion   = 1
	defaultOpenTimeout  = 15 * time.Second
	defaultStallTimeout = 30 * time.Second
)

const (
	transportTCP  = "tcp"
	transportKCP  = "kcp"
	transportICMP = "icmp"

	connectorHTTP  = "http"
	connectorRelay = "relay"
)

type Outbound struct {
	outbound.Adapter
	logger       logger.ContextLogger
	dialer       N.Dialer
	serverAddr   M.Socksaddr
	username     string
	password     string
	authHeader   string
	relay        bool
	newSession   func(ctx context.Context) (session, error)
	muxConfig    *smux.Config
	openTimeout  time.Duration
	stallTimeout time.Duration

	access  sync.Mutex
	session session
	pending *pendingSession
	closed  bool
}

// session is one connection to the node that carries a stream per proxied
// connection: an smux session over TCP or KCP, or a QUIC connection over ICMP.
type session interface {
	OpenStream() (net.Conn, error)
	IsClosed() bool
	Close() error
}

type pendingSession struct {
	done    chan struct{}
	session session
	err     error
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.GostOutboundOptions) (adapter.Outbound, error) {
	if options.Server == "" {
		return nil, E.New("missing server")
	}
	if options.ServerPort == 0 {
		return nil, E.New("missing server_port")
	}
	transport := options.Transport
	if transport == "" {
		transport = transportTCP
	}
	connector := options.Connector
	if connector == "" {
		connector = connectorHTTP
	}
	switch connector {
	case connectorHTTP, connectorRelay:
	default:
		return nil, E.New("unknown connector: ", connector)
	}
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}
	if transport == transportTCP && options.TLS != nil && options.TLS.Enabled {
		tlsConfig, err := tls.NewClientWithOptions(tls.ClientOptions{
			Context:       ctx,
			Logger:        logger,
			ServerAddress: options.Server,
			Options:       *options.TLS,
		})
		if err != nil {
			return nil, err
		}
		outboundDialer = tls.NewDialer(outboundDialer, tlsConfig)
	}
	muxOptions := common.PtrValueOrDefault(options.Mux)
	muxConfig := smux.DefaultConfig()
	muxConfig.Version = defaultMuxVersion
	if transport == transportKCP {
		applyKCPMuxDefaults(muxConfig)
	}
	if muxOptions.Version != 0 {
		muxConfig.Version = muxOptions.Version
	}
	muxConfig.KeepAliveDisabled = muxOptions.KeepAliveDisabled
	if muxOptions.KeepAliveInterval > 0 {
		muxConfig.KeepAliveInterval = time.Duration(muxOptions.KeepAliveInterval)
	}
	if muxOptions.KeepAliveTimeout > 0 {
		muxConfig.KeepAliveTimeout = time.Duration(muxOptions.KeepAliveTimeout)
	}
	if muxOptions.MaxFrameSize > 0 {
		muxConfig.MaxFrameSize = muxOptions.MaxFrameSize
	}
	if muxOptions.MaxReceiveBuffer > 0 {
		muxConfig.MaxReceiveBuffer = muxOptions.MaxReceiveBuffer
	}
	if muxOptions.MaxStreamBuffer > 0 {
		muxConfig.MaxStreamBuffer = muxOptions.MaxStreamBuffer
	}
	err = smux.VerifyConfig(muxConfig)
	if err != nil {
		return nil, E.Cause(err, "mux")
	}
	openTimeout := time.Duration(muxOptions.OpenTimeout)
	if openTimeout <= 0 {
		openTimeout = defaultOpenTimeout
	}
	stallTimeout := time.Duration(muxOptions.StallTimeout)
	if stallTimeout <= 0 {
		stallTimeout = defaultStallTimeout
	}
	var authHeader string
	if options.Username != "" || options.Password != "" {
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(options.Username+":"+options.Password))
	}
	h := &Outbound{
		Adapter:      outbound.NewAdapterWithDialerOptions(C.TypeGost, tag, []string{N.NetworkTCP}, options.DialerOptions),
		logger:       logger,
		dialer:       outboundDialer,
		serverAddr:   options.ServerOptions.Build(),
		username:     options.Username,
		password:     options.Password,
		authHeader:   authHeader,
		relay:        connector == connectorRelay,
		muxConfig:    muxConfig,
		openTimeout:  openTimeout,
		stallTimeout: stallTimeout,
	}
	switch transport {
	case transportTCP:
		h.newSession = h.newTCPSession
	case transportKCP:
		h.newSession, err = h.kcpSessionDialer(common.PtrValueOrDefault(options.KCP))
	case transportICMP:
		h.newSession, err = h.icmpSessionDialer(ctx, options)
	default:
		err = E.New("unknown transport: ", transport)
	}
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateInitialize {
		return nil
	}
	scope.Add(h.Close)
	return nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if N.NetworkName(network) != N.NetworkTCP {
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound connection to ", destination)
	stream, err := h.openStream(ctx)
	if err != nil {
		return nil, err
	}
	var conn net.Conn
	if h.relay {
		conn, err = h.connectRelay(ctx, stream, destination)
	} else {
		conn, err = h.connect(ctx, stream, destination)
	}
	if err != nil {
		stream.Close()
		return nil, err
	}
	return conn, nil
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	h.reset(false)
}

func (h *Outbound) Close() error {
	h.reset(true)
	return nil
}

func (h *Outbound) reset(closed bool) {
	h.access.Lock()
	current := h.session
	h.session = nil
	if closed {
		h.closed = true
	}
	h.access.Unlock()
	if current != nil {
		current.Close()
	}
}

// openStream opens a stream on the shared session. When the session cannot
// open a stream in time, it is dropped and the stream is tried once more on a
// new session.
func (h *Outbound) openStream(ctx context.Context) (net.Conn, error) {
	var lastErr error
	for range 2 {
		current, err := h.getSession(ctx)
		if err != nil {
			return nil, err
		}
		stream, err := h.awaitStream(ctx, current)
		if err == nil {
			return stream, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		lastErr = err
		h.logger.DebugContext(ctx, "drop session to ", h.serverAddr, ": open stream: ", err)
		h.dropSession(current)
	}
	return nil, lastErr
}

func (h *Outbound) awaitStream(ctx context.Context, current session) (net.Conn, error) {
	type result struct {
		stream net.Conn
		err    error
	}
	done := make(chan result, 1)
	go func() {
		stream, err := current.OpenStream()
		done <- result{stream, err}
	}()
	timer := time.NewTimer(h.openTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.stream, r.err
	case <-timer.C:
	case <-ctx.Done():
	}
	go func() {
		if r := <-done; r.err == nil {
			r.stream.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, E.New("open stream: timeout after ", h.openTimeout)
}

// getSession returns the live session, or dials one. Concurrent callers share
// one dial; each waits for it only as long as its own context allows.
func (h *Outbound) getSession(ctx context.Context) (session, error) {
	h.access.Lock()
	if h.closed {
		h.access.Unlock()
		return nil, net.ErrClosed
	}
	if h.session != nil {
		if !h.session.IsClosed() {
			current := h.session
			h.access.Unlock()
			return current, nil
		}
		h.session.Close()
		h.session = nil
	}
	pending := h.pending
	if pending == nil {
		pending = &pendingSession{done: make(chan struct{})}
		h.pending = pending
		go h.dialSession(ctx, pending)
	}
	h.access.Unlock()
	select {
	case <-pending.done:
		return pending.session, pending.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// dialSession dials the session with a context of its own: the caller that
// started it may give up, while others still wait for it.
func (h *Outbound) dialSession(ctx context.Context, pending *pendingSession) {
	dialCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.openTimeout)
	defer cancel()
	current, err := h.newSession(dialCtx)
	h.access.Lock()
	h.pending = nil
	if err == nil && h.closed {
		current.Close()
		current, err = nil, net.ErrClosed
	}
	if err == nil {
		h.session = current
	}
	h.access.Unlock()
	if err != nil {
		h.logger.ErrorContext(ctx, "dial session to ", h.serverAddr, ": ", err)
	}
	pending.session, pending.err = current, err
	close(pending.done)
}

func (h *Outbound) newTCPSession(ctx context.Context) (session, error) {
	conn, err := h.dialer.DialContext(ctx, N.NetworkTCP, h.serverAddr)
	if err != nil {
		return nil, err
	}
	// A dial that finished after its deadline must not leave the deadline
	// set on the conn the session keeps.
	if ctx.Err() != nil {
		conn.Close()
		return nil, ctx.Err()
	}
	return h.newMuxSession(conn)
}

// newMuxSession starts an smux client session over conn and owns conn from
// then on.
func (h *Outbound) newMuxSession(conn net.Conn) (session, error) {
	guarded := &stallGuardConn{Conn: conn, timeout: h.stallTimeout}
	muxSession, err := smux.Client(guarded, h.muxConfig)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &smuxSession{Session: muxSession, conn: conn}, nil
}

func (h *Outbound) dropSession(current session) {
	h.access.Lock()
	if h.session == current {
		h.session = nil
	}
	h.access.Unlock()
	current.Close()
}

type smuxSession struct {
	*smux.Session
	conn net.Conn
}

func (s *smuxSession) OpenStream() (net.Conn, error) {
	stream, err := s.Session.OpenStream()
	if err != nil {
		return nil, err
	}
	return stream, nil
}

func (s *smuxSession) Close() error {
	err := s.Session.Close()
	s.conn.Close()
	return err
}

// connect sends the HTTP CONNECT request of gost's http connector over stream
// and reads the answer.
func (h *Outbound) connect(ctx context.Context, stream net.Conn, destination M.Socksaddr) (net.Conn, error) {
	address := destination.String()
	request := &http.Request{
		Method:     http.MethodConnect,
		URL:        &url.URL{Host: address},
		Host:       address,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
	}
	request.Header.Set("Proxy-Connection", "keep-alive")
	if h.authHeader != "" {
		request.Header.Set("Proxy-Authorization", h.authHeader)
	}
	deadline := time.Now().Add(h.openTimeout)
	if ctxDeadline, loaded := ctx.Deadline(); loaded && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	stream.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() {
		stream.SetDeadline(time.Unix(1, 0))
	})
	defer stop()
	err := request.Write(stream)
	if err != nil {
		return nil, E.Cause(err, "write CONNECT")
	}
	reader := bufio.NewReader(stream)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, E.Cause(err, "read CONNECT response")
	}
	if response.StatusCode != http.StatusOK {
		err = E.New("CONNECT ", address, ": ", response.Status)
		switch response.StatusCode {
		case http.StatusForbidden, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			// The node answered and refused or could not reach the
			// destination (gost: 503 when its dial fails, 403 on bypass):
			// the path to the node works. Any other status, such as 407 or
			// the probe-resistance code on bad credentials, is the node's.
			return nil, adapter.NewDestinationError(err)
		default:
			return nil, err
		}
	}
	if !stop() {
		return nil, ctx.Err()
	}
	stream.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: stream, reader: reader}, nil
	}
	return stream, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.reader.Buffered() > 0 {
		return c.reader.Read(p)
	}
	return c.Conn.Read(p)
}

// stallGuardConn fails a write that cannot complete within timeout. smux
// writes frames from one loop, so a write that never drains would otherwise
// hold every stream of the session while its keepalive, which only watches
// reads, still sees the server's frames. The failed write closes the session.
type stallGuardConn struct {
	net.Conn
	timeout time.Duration
}

func (c *stallGuardConn) Write(p []byte) (int, error) {
	err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout))
	if err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}
