package include

import (
	"context"

	"github.com/pasadizo-net/pasadizo"
	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/certificate"
	"github.com/pasadizo-net/pasadizo/adapter/endpoint"
	"github.com/pasadizo-net/pasadizo/adapter/inbound"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/adapter/service"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/dns"
	"github.com/pasadizo-net/pasadizo/dns/transport"
	"github.com/pasadizo-net/pasadizo/dns/transport/fakeip"
	"github.com/pasadizo-net/pasadizo/dns/transport/hosts"
	"github.com/pasadizo-net/pasadizo/dns/transport/local"
	"github.com/pasadizo-net/pasadizo/dns/transport/mdns"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/pasadizo-net/pasadizo/protocol/anytls"
	"github.com/pasadizo-net/pasadizo/protocol/block"
	"github.com/pasadizo-net/pasadizo/protocol/bridge"
	"github.com/pasadizo-net/pasadizo/protocol/direct"
	"github.com/pasadizo-net/pasadizo/protocol/group"
	"github.com/pasadizo-net/pasadizo/protocol/http"
	"github.com/pasadizo-net/pasadizo/protocol/masque"
	"github.com/pasadizo-net/pasadizo/protocol/mixed"
	"github.com/pasadizo-net/pasadizo/protocol/naive"
	"github.com/pasadizo-net/pasadizo/protocol/redirect"
	"github.com/pasadizo-net/pasadizo/protocol/shadowsocks"
	"github.com/pasadizo-net/pasadizo/protocol/shadowtls"
	"github.com/pasadizo-net/pasadizo/protocol/snell"
	"github.com/pasadizo-net/pasadizo/protocol/socks"
	"github.com/pasadizo-net/pasadizo/protocol/ssh"
	"github.com/pasadizo-net/pasadizo/protocol/tor"
	"github.com/pasadizo-net/pasadizo/protocol/trojan"
	"github.com/pasadizo-net/pasadizo/protocol/tun"
	"github.com/pasadizo-net/pasadizo/protocol/vless"
	"github.com/pasadizo-net/pasadizo/protocol/vmess"
	"github.com/pasadizo-net/pasadizo/service/api"
	originca "github.com/pasadizo-net/pasadizo/service/origin_ca"
	"github.com/pasadizo-net/pasadizo/service/resolved"
	"github.com/pasadizo-net/pasadizo/service/ssmapi"
	E "github.com/sagernet/sing/common/exceptions"
)

func Context(ctx context.Context) context.Context {
	return box.Context(ctx, InboundRegistry(), OutboundRegistry(), EndpointRegistry(), DNSTransportRegistry(), ServiceRegistry(), CertificateProviderRegistry())
}

func InboundRegistry() *inbound.Registry {
	registry := inbound.NewRegistry()

	tun.RegisterInbound(registry)
	redirect.RegisterRedirect(registry)
	redirect.RegisterTProxy(registry)
	direct.RegisterInbound(registry)

	socks.RegisterInbound(registry)
	http.RegisterInbound(registry)
	mixed.RegisterInbound(registry)

	shadowsocks.RegisterInbound(registry)
	snell.RegisterInbound(registry)
	vmess.RegisterInbound(registry)
	trojan.RegisterInbound(registry)
	naive.RegisterInbound(registry)
	shadowtls.RegisterInbound(registry)
	vless.RegisterInbound(registry)
	anytls.RegisterInbound(registry)

	registerQUICInbounds(registry)
	registerCloudflaredInbound(registry)
	registerTailcatInbound(registry)
	registerStubForRemovedInbounds(registry)

	return registry
}

func OutboundRegistry() *outbound.Registry {
	registry := outbound.NewRegistry()

	direct.RegisterOutbound(registry)
	bridge.RegisterOutbound(registry)

	block.RegisterOutbound(registry)

	group.RegisterSelector(registry)
	group.RegisterURLTest(registry)

	socks.RegisterOutbound(registry)
	http.RegisterOutbound(registry)
	shadowsocks.RegisterOutbound(registry)
	snell.RegisterOutbound(registry)
	vmess.RegisterOutbound(registry)
	trojan.RegisterOutbound(registry)
	registerNaiveOutbound(registry)
	tor.RegisterOutbound(registry)
	ssh.RegisterOutbound(registry)
	shadowtls.RegisterOutbound(registry)
	vless.RegisterOutbound(registry)
	anytls.RegisterOutbound(registry)

	registerQUICOutbounds(registry)
	registerTailcatOutbound(registry)
	registerStubForRemovedOutbounds(registry)

	return registry
}

func EndpointRegistry() *endpoint.Registry {
	registry := endpoint.NewRegistry()

	registerWireGuardEndpoint(registry)
	registerOpenConnectEndpoint(registry)
	registerOpenVPNEndpoints(registry)
	masque.RegisterEndpoint(registry)
	registerTailscaleEndpoint(registry)

	return registry
}

func DNSTransportRegistry() *dns.TransportRegistry {
	registry := dns.NewTransportRegistry()

	transport.RegisterTCP(registry)
	transport.RegisterUDP(registry)
	transport.RegisterTLS(registry)
	transport.RegisterHTTPS(registry)
	hosts.RegisterTransport(registry)
	local.RegisterTransport(registry)
	mdns.RegisterTransport(registry)
	fakeip.RegisterTransport(registry)
	resolved.RegisterTransport(registry)

	registerQUICTransports(registry)
	registerDHCPTransport(registry)
	registerTailscaleTransport(registry)
	registerOpenConnectDNSTransport(registry)
	registerOpenVPNDNSTransport(registry)

	return registry
}

func ServiceRegistry() *service.Registry {
	registry := service.NewRegistry()

	api.RegisterService(registry)
	resolved.RegisterService(registry)
	ssmapi.RegisterService(registry)

	registerQUICServices(registry)
	registerDERPService(registry)
	registerCCMService(registry)
	registerOCMService(registry)
	registerOOMKillerService(registry)
	registerUSBIPServices(registry)

	return registry
}

func CertificateProviderRegistry() *certificate.Registry {
	registry := certificate.NewRegistry()

	registerACMECertificateProvider(registry)
	registerTailscaleCertificateProvider(registry)
	originca.RegisterCertificateProvider(registry)

	return registry
}

func registerStubForRemovedInbounds(registry *inbound.Registry) {
	inbound.Register[option.ShadowsocksInboundOptions](registry, C.TypeShadowsocksR, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.ShadowsocksInboundOptions) (adapter.Inbound, error) {
		return nil, E.New("ShadowsocksR is deprecated and removed in sing-box 1.6.0")
	})
}

func registerStubForRemovedOutbounds(registry *outbound.Registry) {
	outbound.Register[option.ShadowsocksROutboundOptions](registry, C.TypeShadowsocksR, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.ShadowsocksROutboundOptions) (adapter.Outbound, error) {
		return nil, E.New("ShadowsocksR is deprecated and removed in sing-box 1.6.0")
	})
	outbound.Register[option.StubOptions](registry, C.TypeWireGuard, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.StubOptions) (adapter.Outbound, error) {
		return nil, E.New("WireGuard outbound is deprecated in sing-box 1.11.0 and removed in sing-box 1.13.0, use WireGuard endpoint instead")
	})
}
