package option

import "github.com/sagernet/sing/common/json/badoption"

type GostOutboundOptions struct {
	DialerOptions
	ServerOptions
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	OutboundTLSOptionsContainer
	Mux *GostMuxOptions `json:"mux,omitempty"`
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
