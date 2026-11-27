//go:build with_openconnect

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/endpoint"
	"github.com/pasadizo-net/pasadizo/dns"
	"github.com/pasadizo-net/pasadizo/protocol/openconnect"
)

func registerOpenConnectEndpoint(registry *endpoint.Registry) {
	openconnect.RegisterEndpoint(registry)
}

func registerOpenConnectDNSTransport(registry *dns.TransportRegistry) {
	openconnect.RegisterDNSTransport(registry)
}
