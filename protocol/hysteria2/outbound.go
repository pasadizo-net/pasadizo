package hysteria2

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/outbound"
	"github.com/pasadizo-net/pasadizo/common/dialer"
	"github.com/pasadizo-net/pasadizo/common/tls"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	qtls "github.com/sagernet/sing-quic"
	congestion "github.com/sagernet/sing-quic/congestion_meta2"
	"github.com/sagernet/sing-quic/hysteria"
	"github.com/sagernet/sing-quic/hysteria2"
	"github.com/sagernet/sing-quic/hysteria2/realm"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.Hysteria2OutboundOptions](registry, C.TypeHysteria2, NewOutbound)
}

var (
	_ adapter.Outbound                = (*Outbound)(nil)
	_ adapter.InterfaceUpdateListener = (*Outbound)(nil)
	_ adapter.IdleConnectionKeeper    = (*Outbound)(nil)
	_ adapter.OutboundWithMultiplex   = (*Outbound)(nil)
)

type Outbound struct {
	outbound.Adapter
	logger  logger.ContextLogger
	clients []*hysteria2.Client
	next    atomic.Uint32

	carefulResume *congestion.CarefulResumeStore
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.Hysteria2OutboundOptions) (adapter.Outbound, error) {
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, C.ErrTLSRequired
	}
	connections := options.Connections
	if connections < 0 {
		return nil, E.New("invalid connections: ", connections)
	} else if connections == 0 {
		connections = 1
	}
	tlsServerAddress, tlsOptions, err := outboundTLSOptions(options)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := tls.NewClientWithOptions(tls.ClientOptions{
		Context:        ctx,
		Logger:         logger,
		ServerAddress:  tlsServerAddress,
		Options:        tlsOptions,
		DomainResolver: options.DomainResolver,
	})
	if err != nil {
		return nil, err
	}
	var salamanderPassword string
	var geckoPassword string
	var geckoMinPacketSize, geckoMaxPacketSize int
	if options.Obfs != nil {
		if options.Obfs.Password == "" {
			return nil, E.New("missing obfs password")
		}
		switch options.Obfs.Type {
		case hysteria2.ObfsTypeSalamander:
			salamanderPassword = options.Obfs.Password
		case hysteria2.ObfsTypeGecko:
			geckoPassword = options.Obfs.Password
			geckoMinPacketSize = options.Obfs.GeckoOptions.MinPacketSize
			geckoMaxPacketSize = options.Obfs.GeckoOptions.MaxPacketSize
		default:
			return nil, E.New("unknown obfs type: ", options.Obfs.Type)
		}
	}
	realmSTUNServersIsDomain := options.Realm != nil && options.Realm.STUNServersIsDomain()
	outboundDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:          ctx,
		Options:          options.DialerOptions,
		RemoteIsDomain:   options.ServerIsDomain() || realmSTUNServersIsDomain,
		ResolverOnDetour: realmSTUNServersIsDomain,
		NewDialer:        realmSTUNServersIsDomain,
	})
	if err != nil {
		return nil, err
	}
	var realmOptions *realm.Options
	if options.Realm != nil {
		var queryOptions adapter.DNSQueryOptions
		if realmSTUNServersIsDomain {
			queryOptions = outboundDialer.(dialer.ResolveDialer).QueryOptions()
		}
		var httpClientTransport adapter.HTTPTransport
		httpClientTransport, err = service.FromContext[adapter.HTTPClientManager](ctx).ResolveTransport(ctx, logger, common.PtrValueOrDefault(options.Realm.HTTPClient))
		if err != nil {
			return nil, E.Cause(err, "create realm http client")
		}
		dnsRouter := service.FromContext[adapter.DNSRouter](ctx)
		realmOptions = &realm.Options{
			ServerURL:   options.Realm.ServerURL,
			Token:       options.Realm.Token,
			RealmID:     options.Realm.RealmID,
			STUNServers: options.Realm.STUNServers,
			HTTPClient:  &http.Client{Transport: httpClientTransport},
			Resolver: func(ctx context.Context, host string, ipv4, ipv6 bool) ([]netip.Addr, error) {
				dnsOptions := queryOptions
				switch {
				case ipv4 && !ipv6:
					dnsOptions.Strategy = C.DomainStrategyIPv4Only
				case !ipv4 && ipv6:
					dnsOptions.Strategy = C.DomainStrategyIPv6Only
				}
				return dnsRouter.Lookup(ctx, host, dnsOptions)
			},
			Logger:    logger,
			IPVersion: options.Realm.IPVersion,
		}
		if options.Realm.PortMapping != nil && options.Realm.PortMapping.Enabled {
			realmOptions.PortMapping = &realm.PortMappingOptions{
				Timeout:  time.Duration(options.Realm.PortMapping.Timeout),
				Lifetime: time.Duration(options.Realm.PortMapping.Lifetime),
			}
		}
	}
	networkList := options.Network.Build()
	// One store for every connection of the pool: they share the path, and
	// only one of them at a time resumes from the saved parameters.
	carefulResume, err := newCarefulResumeStore(ctx, options.CarefulResume)
	if err != nil {
		return nil, err
	}
	clientOptions := hysteria2.ClientOptions{
		Context:            ctx,
		Dialer:             outboundDialer,
		Logger:             logger,
		BrutalDebug:        options.BrutalDebug,
		ServerAddress:      options.ServerOptions.Build(),
		ServerPorts:        options.ServerPorts,
		HopInterval:        time.Duration(options.HopInterval),
		HopIntervalMax:     time.Duration(options.HopIntervalMax),
		SendBPS:            splitBandwidth(options.UpMbps, connections),
		ReceiveBPS:         splitBandwidth(options.DownMbps, connections),
		SalamanderPassword: salamanderPassword,
		GeckoPassword:      geckoPassword,
		GeckoMinPacketSize: geckoMinPacketSize,
		GeckoMaxPacketSize: geckoMaxPacketSize,
		Password:           options.Password,
		TLSConfig:          tlsConfig,
		QUICOptions: qtls.QUICOptions{
			IdleTimeout:             options.IdleTimeout.Build(),
			KeepAlivePeriod:         options.KeepAlivePeriod.Build(),
			StreamReceiveWindow:     options.StreamReceiveWindow.Value(),
			ConnectionReceiveWindow: options.ConnectionReceiveWindow.Value(),
			MaxConcurrentStreams:    options.MaxConcurrentStreams,
			InitialPacketSize:       options.InitialPacketSize,
			DisablePathMTUDiscovery: options.DisablePathMTUDiscovery,
		},
		UDPDisabled:   !common.Contains(networkList, N.NetworkUDP),
		BBRProfile:    options.BBRProfile,
		ChromeParrot:  !options.DisableChromeParrot,
		RealmOptions:  realmOptions,
		CarefulResume: carefulResume,
	}
	clients := make([]*hysteria2.Client, connections)
	for i := range clients {
		clients[i], err = hysteria2.NewClient(clientOptions)
		if err != nil {
			return nil, err
		}
	}
	return &Outbound{
		Adapter:       outbound.NewAdapterWithDialerOptions(C.TypeHysteria2, tag, networkList, options.DialerOptions),
		logger:        logger,
		clients:       clients,
		carefulResume: carefulResume,
	}, nil
}

// splitBandwidth divides the configured bandwidth of the outbound evenly
// between its connections, so that their sum stays at the configured value.
func splitBandwidth(mbps int, connections int) uint64 {
	return uint64(mbps*hysteria.MbpsToBps) / uint64(connections)
}

// pickClient returns the client for a new stream or UDP session, in round-robin order.
func (h *Outbound) pickClient() *hysteria2.Client {
	if len(h.clients) == 1 {
		return h.clients[0]
	}
	return h.clients[(h.next.Add(1)-1)%uint32(len(h.clients))]
}

func outboundTLSOptions(options option.Hysteria2OutboundOptions) (string, option.OutboundTLSOptions, error) {
	tlsOptions := common.PtrValueOrDefault(options.TLS)
	if options.Realm == nil {
		return options.Server, tlsOptions, nil
	}
	if options.Server != "" || options.ServerPort != 0 || len(options.ServerPorts) > 0 {
		return "", tlsOptions, E.New("realm conflicts with server, server_port, and server_ports")
	}
	serverURL, err := url.Parse(options.Realm.ServerURL)
	if err != nil {
		return "", tlsOptions, E.Cause(err, "parse realm server_url")
	}
	serverName := serverURL.Hostname()
	if serverName == "" {
		return "", tlsOptions, E.New("missing host in realm server_url")
	}
	return serverName, tlsOptions, nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		return h.pickClient().DialConn(ctx, destination)
	case N.NetworkUDP:
		conn, err := h.ListenPacket(ctx, destination)
		if err != nil {
			return nil, err
		}
		return bufio.NewBindPacketConn(conn, destination), nil
	default:
		return nil, E.New("unsupported network: ", network)
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	return h.pickClient().ListenPacket(ctx)
}

func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	err := E.New("network changed")
	for _, client := range h.clients {
		client.CloseWithError(err)
	}
}

func (h *Outbound) MultiplexEnabled() bool {
	return true
}

func (h *Outbound) SetKeepIdleConnections(keep bool) {
	for _, client := range h.clients {
		client.SetKeepIdleConnections(keep)
	}
}

func (h *Outbound) CloseIdleConnections() {
	for _, client := range h.clients {
		client.CloseIdleConnections()
	}
}

func (h *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateInitialize {
		return nil
	}
	scope.Add(func() error {
		for _, client := range h.clients {
			client.CloseWithError(os.ErrClosed)
		}
		return nil
	})
	if h.carefulResume != nil {
		scope.Add(h.carefulResume.Close)
	}
	return nil
}
