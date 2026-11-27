//go:build with_openvpn

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/endpoint"
	"github.com/pasadizo-net/pasadizo/dns"
	"github.com/pasadizo-net/pasadizo/protocol/openvpn"
)

func registerOpenVPNEndpoints(registry *endpoint.Registry) {
	openvpn.RegisterEndpoint(registry)
}

func registerOpenVPNDNSTransport(registry *dns.TransportRegistry) {
	openvpn.RegisterDNSTransport(registry)
}
