//go:build with_quic

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/inbound"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/adapter/service"
	"github.com/pasadizo-net/pasadizo/dns"
	"github.com/pasadizo-net/pasadizo/dns/transport/quic"
	"github.com/pasadizo-net/pasadizo/protocol/hysteria"
	"github.com/pasadizo-net/pasadizo/protocol/hysteria2"
	_ "github.com/pasadizo-net/pasadizo/protocol/naive/quic"
	"github.com/pasadizo-net/pasadizo/protocol/tuic"
	_ "github.com/pasadizo-net/pasadizo/transport/v2rayquic"
)

func registerQUICInbounds(registry *inbound.Registry) {
	hysteria.RegisterInbound(registry)
	tuic.RegisterInbound(registry)
	hysteria2.RegisterInbound(registry)
}

func registerQUICOutbounds(registry *outbound.Registry) {
	hysteria.RegisterOutbound(registry)
	tuic.RegisterOutbound(registry)
	hysteria2.RegisterOutbound(registry)
}

func registerQUICTransports(registry *dns.TransportRegistry) {
	quic.RegisterTransport(registry)
	quic.RegisterHTTP3Transport(registry)
}

func registerQUICServices(registry *service.Registry) {
	hysteria2.RegisterRealmService(registry)
}
