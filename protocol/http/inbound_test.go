package http_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"testing"
	"time"

	box "github.com/pasadizo-net/pasadizo"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/include"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func startProxy(t *testing.T, exempt ...string) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	var prefixes badoption.Listable[netip.Prefix]
	for _, prefix := range exempt {
		prefixes = append(prefixes, netip.MustParsePrefix(prefix))
	}
	listen := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			Log: &option.LogOptions{Level: "warning"},
			Inbounds: []option.Inbound{{
				Type: C.TypeHTTP,
				Tag:  "http-in",
				Options: &option.HTTPInboundOptions{
					ListenOptions:          option.ListenOptions{Listen: &listen, ListenPort: port},
					Users:                  []auth.User{{Username: "user", Password: "pass"}},
					AuthExemptSourceIPCIDR: prefixes,
				},
			}},
			Outbounds: []option.Outbound{{Type: C.TypeDirect, Tag: "direct", Options: &option.DirectOutboundOptions{}}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, instance.Start())
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
}

func get(t *testing.T, proxy *url.URL, target string) int {
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxy)},
		Timeout:   5 * time.Second,
	}
	response, err := client.Get(target)
	require.NoError(t, err)
	response.Body.Close()
	return response.StatusCode
}

func TestHTTPAuthExemptSource(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	exempt := startProxy(t, "127.0.0.0/8")
	require.Equal(t, http.StatusNoContent, get(t, &url.URL{Scheme: "http", Host: exempt}, target.URL))

	other := startProxy(t, "10.0.0.0/8")
	require.Equal(t, http.StatusProxyAuthRequired, get(t, &url.URL{Scheme: "http", Host: other}, target.URL))
	require.Equal(t, http.StatusNoContent, get(t, &url.URL{Scheme: "http", Host: other, User: url.UserPassword("user", "pass")}, target.URL))
	// A forwarding header must not buy an exemption.
	client := &http.Client{Transport: &http.Transport{
		Proxy:              http.ProxyURL(&url.URL{Scheme: "http", Host: other}),
		ProxyConnectHeader: http.Header{"X-Forwarded-For": {"10.1.2.3"}},
	}, Timeout: 5 * time.Second}
	request, _ := http.NewRequest(http.MethodGet, target.URL, nil)
	request.Header.Set("X-Forwarded-For", "10.1.2.3")
	response, err := client.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusProxyAuthRequired, response.StatusCode)
}
