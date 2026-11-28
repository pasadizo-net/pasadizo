//go:build !with_dhcp

package include

import (
	"context"

	"github.com/pasadizo-net/pasadizo/adapter"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/dns"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	E "github.com/sagernet/sing/common/exceptions"
)

func registerDHCPTransport(registry *dns.TransportRegistry) {
	dns.RegisterTransport[option.DHCPDNSServerOptions](registry, C.DNSTypeDHCP, func(ctx context.Context, logger log.ContextLogger, tag string, options option.DHCPDNSServerOptions) (adapter.DNSTransport, error) {
		return nil, E.New(`DHCP is not included in this build, rebuild with -tags with_dhcp`)
	})
}
