//go:build with_quic

package masque

import (
	"context"
	stdTLS "crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/common/tls"
	"github.com/pasadizo-net/pasadizo/option"
	transportHTTP "github.com/pasadizo-net/pasadizo/transport/http"
	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	N "github.com/sagernet/sing/common/network"
)

type warpTestRequest struct {
	protocol        string
	capsuleProtocol string
	settings        *http3.Settings
	capsuleBytes    int
}

type warpTestHandler struct {
	configurations chan Configuration
}

func (h *warpTestHandler) UpdateConfiguration(configuration Configuration) error {
	h.configurations <- configuration
	return nil
}

func (h *warpTestHandler) WriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	buf.ReleaseMulti(packetBuffers)
	return nil
}

func (h *warpTestHandler) WaitWriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	buf.ReleaseMulti(packetBuffers)
	return nil
}

func (h *warpTestHandler) FrontHeadroom() int {
	return 0
}

func startWARPTestServer(t *testing.T) (uint16, <-chan warpTestRequest) {
	t.Helper()
	certificate, err := tls.GenerateKeyPair(nil, nil, time.Now, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan warpTestRequest, 1)
	server := &http3.Server{
		TLSConfig:       http3.ConfigureTLSConfig(&stdTLS.Config{Certificates: []stdTLS.Certificate{*certificate}}),
		QUICConfig:      &quic.Config{EnableDatagrams: true},
		EnableDatagrams: true,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			settingser := writer.(http3.Settingser)
			<-settingser.ReceivedSettings()
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			stream := writer.(http3.HTTPStreamer).HTTPStream()
			_ = stream.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, readErr := stream.Read(make([]byte, 64))
			if readErr != nil && !errors.Is(readErr, os.ErrDeadlineExceeded) {
				t.Error(readErr)
			}
			requests <- warpTestRequest{
				protocol:        request.Proto,
				capsuleProtocol: request.Header.Get("Capsule-Protocol"),
				settings:        settingser.Settings(),
				capsuleBytes:    n,
			}
			<-request.Context().Done()
		}),
	}
	go server.Serve(packetConn)
	t.Cleanup(func() {
		server.Close()
		packetConn.Close()
	})
	return uint16(packetConn.LocalAddr().(*net.UDPAddr).Port), requests
}

func startWARPTestClient(t *testing.T, serverPort uint16, warp bool, address []netip.Prefix) *warpTestHandler {
	t.Helper()
	ctx := context.Background()
	var http3Settings map[uint64]uint64
	if warp {
		http3Settings = WARPHTTP3Settings()
	}
	httpClient, err := transportHTTP.NewClientWithTLS(ctx, logger.NOP(), N.SystemDialer, option.ServerOptions{
		Server:     "127.0.0.1",
		ServerPort: serverPort,
	}, option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "example.org",
		Insecure:   true,
	}, nil, transportHTTP.ClientOptions{
		Authority:              "cloudflareaccess.com",
		Version:                3,
		DisableVersionFallback: true,
		HTTP3Settings:          http3Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := &warpTestHandler{configurations: make(chan Configuration, 1)}
	client, err := NewClient(ClientOptions{
		Context:    ctx,
		Logger:     logger.NOP(),
		HTTPClient: httpClient,
		Path:       "/",
		Address:    address,
		WARP:       warp,
		Handler:    handler,
	})
	if err != nil {
		httpClient.Close()
		t.Fatal(err)
	}
	client.Start()
	t.Cleanup(func() {
		client.Close()
	})
	return handler
}

func receiveWARPTestRequest(t *testing.T, requests <-chan warpTestRequest) warpTestRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for CONNECT-IP request")
		return warpTestRequest{}
	}
}

func TestClientWARPRequest(t *testing.T) {
	t.Parallel()
	address := []netip.Prefix{
		netip.MustParsePrefix("172.16.0.2/32"),
		netip.MustParsePrefix("2606:4700:110:8a36::1/128"),
	}
	serverPort, requests := startWARPTestServer(t)
	handler := startWARPTestClient(t, serverPort, true, address)
	request := receiveWARPTestRequest(t, requests)
	if request.protocol != "cf-connect-ip" {
		t.Fatalf("unexpected :protocol %q", request.protocol)
	}
	if request.capsuleProtocol != "?1" {
		t.Fatalf("unexpected Capsule-Protocol %q", request.capsuleProtocol)
	}
	if !request.settings.EnableDatagrams {
		t.Fatal("SETTINGS_H3_DATAGRAM is not sent")
	}
	if value, loaded := request.settings.Other[settingH3Datagram00]; !loaded || value != 1 {
		t.Fatalf("SETTINGS_H3_DATAGRAM_00 is not sent: %v", request.settings.Other)
	}
	if request.capsuleBytes != 0 {
		t.Fatalf("client with a static address sent %d capsule bytes", request.capsuleBytes)
	}
	select {
	case configuration := <-handler.configurations:
		if !slices.Equal(configuration.Address, address) {
			t.Fatalf("unexpected address %v", configuration.Address)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for configuration")
	}
}

func TestClientStandardRequest(t *testing.T) {
	t.Parallel()
	serverPort, requests := startWARPTestServer(t)
	startWARPTestClient(t, serverPort, false, nil)
	request := receiveWARPTestRequest(t, requests)
	if request.protocol != "connect-ip" {
		t.Fatalf("unexpected :protocol %q", request.protocol)
	}
	if _, loaded := request.settings.Other[settingH3Datagram00]; loaded {
		t.Fatal("SETTINGS_H3_DATAGRAM_00 is sent without WARP mode")
	}
	if request.capsuleBytes == 0 {
		t.Fatal("ADDRESS_REQUEST capsule is not sent")
	}
}
