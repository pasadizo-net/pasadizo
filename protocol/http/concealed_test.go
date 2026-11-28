package http_test

import (
	std_bufio "bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	box "github.com/pasadizo-net/pasadizo"
	"github.com/pasadizo-net/pasadizo/common/concealedauth"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/include"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

// Generated with `pasadizo generate concealed-auth-keypair`.
const (
	concealedPrivateKey = "MC4CAQAwBQYDK2VwBCIEILoeVQfoLJ03VcM2eskAHWh2CBxSXuEbaRad0lUFvPr3"
	concealedPublicKey  = "gEyHTN0vVhjoVXysQtQ57-kp2wH05Um1F68S5xPIr1A"
	otherPrivateKey     = "MC4CAQAwBQYDK2VwBCIEIBfsvzgDLLwuWcqx+H4AsaCCXOhcT1Tv5kZTxvGc/3I3"
)

func freePort(t *testing.T) uint16 {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func selfSignedCertificate(t *testing.T) (string, string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func startBox(t *testing.T, options option.Options) {
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	options.Log = &option.LogOptions{Level: "panic"}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, err)
	require.NoError(t, instance.Start())
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})
}

// startConcealedProxy starts an HTTP proxy over TLS that accepts the key
// concealedPublicKey, and returns its port.
func startConcealedProxy(t *testing.T, fallback *option.ServerOptions) uint16 {
	port := freePort(t)
	certificate, key := selfSignedCertificate(t)
	listen := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	startBox(t, option.Options{
		Inbounds: []option.Inbound{{
			Type: C.TypeHTTP,
			Tag:  "http-in",
			Options: &option.HTTPInboundOptions{
				ListenOptions: option.ListenOptions{Listen: &listen, ListenPort: port},
				ConcealedAuth: &option.ConcealedAuthInboundOptions{
					Users:    []option.ConcealedAuthUser{{Name: "parham", KeyID: "house", PublicKey: concealedPublicKey}},
					Fallback: fallback,
				},
				Version: []int{1, 2, 3},
				InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{TLS: &option.InboundTLSOptions{
					Enabled:     true,
					Certificate: []string{certificate},
					Key:         []string{key},
				}},
			},
		}},
		Outbounds: []option.Outbound{{Type: C.TypeDirect, Tag: "direct", Options: &option.DirectOutboundOptions{}}},
	})
	return port
}

// startConcealedClient starts a mixed proxy whose traffic leaves through an
// HTTP outbound with concealed authentication, and returns the mixed proxy.
func startConcealedClient(t *testing.T, serverPort uint16, privateKey string, version int) *url.URL {
	port := freePort(t)
	listen := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	startBox(t, option.Options{
		Inbounds: []option.Inbound{{
			Type:    C.TypeMixed,
			Tag:     "mixed-in",
			Options: &option.HTTPMixedInboundOptions{ListenOptions: option.ListenOptions{Listen: &listen, ListenPort: port}},
		}},
		Outbounds: []option.Outbound{{
			Type: C.TypeHTTP,
			Tag:  "http-out",
			Options: &option.HTTPOutboundOptions{
				ServerOptions:          option.ServerOptions{Server: "127.0.0.1", ServerPort: serverPort},
				ConcealedAuth:          &option.ConcealedAuthOutboundOptions{KeyID: "house", PrivateKey: privateKey},
				Version:                version,
				DisableVersionFallback: true,
				OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
					Enabled:  true,
					Insecure: true,
				}},
			},
		}},
	})
	return &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))}
}

func newTarget(t *testing.T) *httptest.Server {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	return target
}

func TestConcealedAuthOutbound(t *testing.T) {
	target := newTarget(t)
	serverPort := startConcealedProxy(t, nil)
	for _, version := range []int{1, 2, 3} {
		t.Run("HTTP/"+strconv.Itoa(version), func(t *testing.T) {
			proxy := startConcealedClient(t, serverPort, concealedPrivateKey, version)
			require.Equal(t, http.StatusNoContent, get(t, proxy, target.URL))
		})
	}
}

func TestConcealedAuthWrongKey(t *testing.T) {
	target := newTarget(t)
	serverPort := startConcealedProxy(t, nil)
	for _, version := range []int{1, 2, 3} {
		t.Run("HTTP/"+strconv.Itoa(version), func(t *testing.T) {
			proxy := startConcealedClient(t, serverPort, otherPrivateKey, version)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 5 * time.Second}
			response, err := client.Get(target.URL)
			if err == nil {
				response.Body.Close()
				require.NotEqual(t, http.StatusNoContent, response.StatusCode)
			}
		})
	}
}

func dialTLS(t *testing.T, port uint16, nextProtos ...string) *tls.Conn {
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         nextProtos,
	})
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// rawRequest sends one HTTP/1.1 request on conn and reads the response.
func rawRequest(t *testing.T, conn net.Conn, request string) (*http.Response, string) {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err := io.WriteString(conn, request)
	require.NoError(t, err)
	response, err := http.ReadResponse(std_bufio.NewReader(conn), nil)
	require.NoError(t, err)
	var body []byte
	if response.StatusCode != http.StatusOK || response.ContentLength >= 0 {
		body, _ = io.ReadAll(response.Body)
	}
	response.Body.Close()
	return response, string(body)
}

func getHTTP2(t *testing.T, port uint16, header http.Header) (int, string) {
	client := &http.Client{Transport: &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 5 * time.Second}
	request, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:"+strconv.Itoa(int(port))+"/", nil)
	require.NoError(t, err)
	request.Header = header
	response, err := client.Do(request)
	require.NoError(t, err)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return response.StatusCode, string(body)
}

func TestConcealedAuthProbeGetsNotFound(t *testing.T) {
	target := newTarget(t)
	port := startConcealedProxy(t, nil)
	targetHost := strings.TrimPrefix(target.URL, "http://")
	for _, request := range []string{
		"GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n",
		"GET " + target.URL + "/ HTTP/1.1\r\nHost: " + targetHost + "\r\n\r\n",
		"CONNECT " + targetHost + " HTTP/1.1\r\nHost: " + targetHost + "\r\n\r\n",
		"CONNECT " + targetHost + " HTTP/1.1\r\nHost: " + targetHost + "\r\nProxy-Authorization: Basic dXNlcjpwYXNz\r\n\r\n",
		"CONNECT " + targetHost + " HTTP/1.1\r\nHost: " + targetHost + "\r\nProxy-Authorization: Concealed k=aG91c2U, a=YQ, s=2055, v=YQ, p=YQ\r\n\r\n",
	} {
		response, body := rawRequest(t, dialTLS(t, port), request)
		require.Equal(t, http.StatusNotFound, response.StatusCode, request)
		require.Equal(t, "Not Found\n", body)
		require.Empty(t, response.Header.Get("Proxy-Authenticate"))
	}
	status, body := getHTTP2(t, port, nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "Not Found\n", body)
}

func TestConcealedAuthProbeGetsFallback(t *testing.T) {
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx")
		io.WriteString(w, "welcome to "+r.Host+r.URL.Path)
	}))
	defer fallback.Close()
	fallbackPort := uint16(fallback.Listener.Addr().(*net.TCPAddr).Port)
	port := startConcealedProxy(t, &option.ServerOptions{Server: "127.0.0.1", ServerPort: fallbackPort})

	conn := dialTLS(t, port)
	response, body := rawRequest(t, conn, "GET /index.html HTTP/1.1\r\nHost: example.org\r\n\r\n")
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "nginx", response.Header.Get("Server"))
	require.Equal(t, "welcome to example.org/index.html", body)
	// The relayed connection stays the fallback's for the next request.
	response, body = rawRequest(t, conn, "GET /second HTTP/1.1\r\nHost: example.org\r\n\r\n")
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "welcome to example.org/second", body)

	status, body := getHTTP2(t, port, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "welcome to 127.0.0.1:"+strconv.Itoa(int(port))+"/", body)
}

func TestConcealedAuthProofBoundToConnection(t *testing.T) {
	target := newTarget(t)
	port := startConcealedProxy(t, nil)
	targetHost := strings.TrimPrefix(target.URL, "http://")
	targetPort, err := strconv.ParseUint(targetHost[strings.LastIndexByte(targetHost, ':')+1:], 10, 16)
	require.NoError(t, err)
	signer, err := concealedauth.NewSigner("house", concealedPrivateKey, "")
	require.NoError(t, err)

	first := dialTLS(t, port)
	state := first.ConnectionState()
	authorization, err := signer.Authorization(state.ExportKeyingMaterial, concealedauth.Origin{Scheme: "https", Host: "127.0.0.1", Port: uint16(targetPort)})
	require.NoError(t, err)
	request := "CONNECT " + targetHost + " HTTP/1.1\r\nHost: " + targetHost + "\r\nProxy-Authorization: " + authorization + "\r\n\r\n"
	response, _ := rawRequest(t, first, request)
	require.Equal(t, http.StatusOK, response.StatusCode)

	// The same header on another TLS session is a replay and gets the decoy.
	response, body := rawRequest(t, dialTLS(t, port), request)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	require.Equal(t, "Not Found\n", body)

	// So does a proof for another target on its own session.
	second := dialTLS(t, port)
	state = second.ConnectionState()
	authorization, err = signer.Authorization(state.ExportKeyingMaterial, concealedauth.Origin{Scheme: "https", Host: "127.0.0.1", Port: 1})
	require.NoError(t, err)
	response, _ = rawRequest(t, second, "CONNECT "+targetHost+" HTTP/1.1\r\nHost: "+targetHost+"\r\nProxy-Authorization: "+authorization+"\r\n\r\n")
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}
