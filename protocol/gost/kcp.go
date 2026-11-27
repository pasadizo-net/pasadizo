package gost

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"net"

	"github.com/pasadizo-net/pasadizo/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/smux"

	"github.com/golang/snappy"
	"github.com/xtaci/kcp-go/v5"
)

// The defaults of gost's kcp dialer (x/internal/util/kcp). Both ends must use
// the same key, crypt, data_shard, parity_shard and compression.
const (
	defaultKCPKey          = "it's a secrect"
	defaultKCPCrypt        = "aes"
	defaultKCPMode         = "fast"
	defaultKCPSalt         = "kcp-go"
	defaultKCPMTU          = 1350
	defaultKCPWindow       = 1024
	defaultKCPDataShard    = 10
	defaultKCPParityShard  = 3
	defaultKCPSocketBuffer = 4194304
	// gost's kcp dialer sets smux's MaxStreamBuffer to half the socket
	// buffer.
	defaultKCPStreamBuffer = defaultKCPSocketBuffer / 2
)

func applyKCPMuxDefaults(config *smux.Config) {
	config.MaxReceiveBuffer = defaultKCPSocketBuffer
	config.MaxStreamBuffer = defaultKCPStreamBuffer
}

type kcpConfig struct {
	block        kcp.BlockCrypt
	dataShard    int
	parityShard  int
	mtu          int
	sendWindow   int
	recvWindow   int
	socketBuffer int
	noDelay      int
	interval     int
	resend       int
	noCongestion int
	compression  bool
}

func newKCPConfig(options option.GostKCPOptions) (*kcpConfig, error) {
	key := options.Key
	if key == "" {
		key = defaultKCPKey
	}
	crypt := options.Crypt
	if crypt == "" {
		crypt = defaultKCPCrypt
	}
	block, err := kcpBlockCrypt(key, crypt)
	if err != nil {
		return nil, err
	}
	config := &kcpConfig{
		block:        block,
		dataShard:    options.DataShard,
		parityShard:  options.ParityShard,
		mtu:          options.MTU,
		sendWindow:   options.SendWindow,
		recvWindow:   options.ReceiveWindow,
		socketBuffer: options.SocketBuffer,
		compression:  !options.NoCompression,
	}
	if config.dataShard == 0 && config.parityShard == 0 {
		config.dataShard, config.parityShard = defaultKCPDataShard, defaultKCPParityShard
	}
	if config.mtu == 0 {
		config.mtu = defaultKCPMTU
	}
	if config.sendWindow == 0 {
		config.sendWindow = defaultKCPWindow
	}
	if config.recvWindow == 0 {
		config.recvWindow = defaultKCPWindow
	}
	if config.socketBuffer == 0 {
		config.socketBuffer = defaultKCPSocketBuffer
	}
	mode := options.Mode
	if mode == "" {
		mode = defaultKCPMode
	}
	switch mode {
	case "normal":
		config.noDelay, config.interval, config.resend, config.noCongestion = 0, 40, 2, 1
	case "fast":
		config.noDelay, config.interval, config.resend, config.noCongestion = 0, 30, 2, 1
	case "fast2":
		config.noDelay, config.interval, config.resend, config.noCongestion = 1, 20, 2, 1
	case "fast3":
		config.noDelay, config.interval, config.resend, config.noCongestion = 1, 10, 2, 1
	default:
		return nil, E.New("unknown kcp mode: ", mode)
	}
	return config, nil
}

// kcpBlockCrypt derives the key as gost and kcptun do: PBKDF2-SHA1 of the
// key with the salt "kcp-go", 4096 rounds, 32 bytes.
func kcpBlockCrypt(key, crypt string) (kcp.BlockCrypt, error) {
	pass, err := pbkdf2.Key(sha1.New, key, []byte(defaultKCPSalt), 4096, 32)
	if err != nil {
		return nil, err
	}
	switch crypt {
	case "aes":
		return kcp.NewAESBlockCrypt(pass)
	case "aes-128":
		return kcp.NewAESBlockCrypt(pass[:16])
	case "aes-192":
		return kcp.NewAESBlockCrypt(pass[:24])
	case "sm4":
		return kcp.NewSM4BlockCrypt(pass[:16])
	case "tea":
		return kcp.NewTEABlockCrypt(pass[:16])
	case "xor":
		return kcp.NewSimpleXORBlockCrypt(pass)
	case "none":
		return kcp.NewNoneBlockCrypt(pass)
	case "blowfish":
		return kcp.NewBlowfishBlockCrypt(pass)
	case "twofish":
		return kcp.NewTwofishBlockCrypt(pass)
	case "cast5":
		return kcp.NewCast5BlockCrypt(pass[:16])
	case "3des":
		return kcp.NewTripleDESBlockCrypt(pass[:24])
	case "xtea":
		return kcp.NewXTEABlockCrypt(pass[:16])
	case "salsa20":
		return kcp.NewSalsa20BlockCrypt(pass)
	default:
		return nil, E.New("unknown kcp crypt: ", crypt)
	}
}

func (h *Outbound) kcpSessionDialer(options option.GostKCPOptions) (func(ctx context.Context) (session, error), error) {
	config, err := newKCPConfig(options)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (session, error) {
		return h.newKCPSession(ctx, config)
	}, nil
}

// newKCPSession opens one KCP session to the node, compressed with snappy
// unless disabled, and starts smux over it, as gost's kcp dialer does.
func (h *Outbound) newKCPSession(ctx context.Context, config *kcpConfig) (session, error) {
	packetConn, err := h.dialer.ListenPacket(ctx, h.serverAddr)
	if err != nil {
		return nil, err
	}
	var remoteAddr net.Addr = h.serverAddr
	if h.serverAddr.IsIP() {
		remoteAddr = h.serverAddr.UDPAddr()
	}
	var conversation [4]byte
	rand.Read(conversation[:])
	// The session owns packetConn and closes it with itself.
	kcpConn, err := kcp.NewConn4(binary.LittleEndian.Uint32(conversation[:]), remoteAddr, config.block, config.dataShard, config.parityShard, true, packetConn)
	if err != nil {
		packetConn.Close()
		return nil, err
	}
	//nolint:staticcheck // gost sets stream mode, deprecated or not.
	kcpConn.SetStreamMode(true)
	kcpConn.SetWriteDelay(false)
	kcpConn.SetNoDelay(config.noDelay, config.interval, config.resend, config.noCongestion)
	kcpConn.SetWindowSize(config.sendWindow, config.recvWindow)
	kcpConn.SetMtu(config.mtu)
	kcpConn.SetReadBuffer(config.socketBuffer)
	kcpConn.SetWriteBuffer(config.socketBuffer)
	var conn net.Conn = kcpConn
	if config.compression {
		conn = newSnappyConn(kcpConn)
	}
	return h.newMuxSession(conn)
}

// snappyConn is gost's kcp compression: a snappy stream, flushed after every
// write.
type snappyConn struct {
	net.Conn
	writer *snappy.Writer
	reader *snappy.Reader
}

func newSnappyConn(conn net.Conn) *snappyConn {
	return &snappyConn{
		Conn:   conn,
		writer: snappy.NewBufferedWriter(conn),
		reader: snappy.NewReader(conn),
	}
}

func (c *snappyConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *snappyConn) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	if err != nil {
		return n, err
	}
	return n, c.writer.Flush()
}
