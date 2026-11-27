package option

import "github.com/sagernet/sing/common/json/badoption"

type GostOutboundOptions struct {
	DialerOptions
	ServerOptions
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Transport is the listener of the gost service: "tcp" (mtls or mtcp,
	// the default), "kcp" or "icmp".
	Transport string `json:"transport,omitempty" enum:"tcp,kcp,icmp"`
	// Connector is the handler of the gost service: "http" (the default) or
	// "relay".
	Connector string `json:"connector,omitempty" enum:"http,relay"`
	OutboundTLSOptionsContainer
	Mux  *GostMuxOptions  `json:"mux,omitempty"`
	KCP  *GostKCPOptions  `json:"kcp,omitempty"`
	ICMP *GostICMPOptions `json:"icmp,omitempty"`
}

type GostMuxOptions struct {
	Version           int                `json:"version,omitempty" enum:"1,2"`
	KeepAliveDisabled bool               `json:"keep_alive_disabled,omitempty"`
	KeepAliveInterval badoption.Duration `json:"keep_alive_interval,omitempty"`
	KeepAliveTimeout  badoption.Duration `json:"keep_alive_timeout,omitempty"`
	MaxFrameSize      int                `json:"max_frame_size,omitempty"`
	MaxReceiveBuffer  int                `json:"max_receive_buffer,omitempty"`
	MaxStreamBuffer   int                `json:"max_stream_buffer,omitempty"`
	OpenTimeout       badoption.Duration `json:"open_timeout,omitempty"`
	StallTimeout      badoption.Duration `json:"stall_timeout,omitempty"`
}

// GostKCPOptions are the kcp.* metadata of gost's kcp dialer.
type GostKCPOptions struct {
	Key           string `json:"key,omitempty"`
	Crypt         string `json:"crypt,omitempty"`
	Mode          string `json:"mode,omitempty" enum:"normal,fast,fast2,fast3"`
	MTU           int    `json:"mtu,omitempty"`
	SendWindow    int    `json:"send_window,omitempty"`
	ReceiveWindow int    `json:"receive_window,omitempty"`
	DataShard     int    `json:"data_shard,omitempty"`
	ParityShard   int    `json:"parity_shard,omitempty"`
	SocketBuffer  int    `json:"socket_buffer,omitempty"`
	NoCompression bool   `json:"no_compression,omitempty"`
}

// GostICMPOptions are the metadata of gost's icmp dialer.
type GostICMPOptions struct {
	KeepAlivePeriod badoption.Duration `json:"keep_alive_period,omitempty"`
	IdleTimeout     badoption.Duration `json:"idle_timeout,omitempty"`
}
