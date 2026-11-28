//go:build with_dhcp

package include

import (
	"github.com/pasadizo-net/pasadizo/dns"
	"github.com/pasadizo-net/pasadizo/dns/transport/dhcp"
)

func registerDHCPTransport(registry *dns.TransportRegistry) {
	dhcp.RegisterTransport(registry)
}
