package masque_test

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
	"net/netip"
	"strconv"
	"testing"
	"time"

	box "github.com/pasadizo-net/pasadizo"
	"github.com/pasadizo-net/pasadizo/common/concealedauth"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/include"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// Generated with `pasadizo generate concealed-auth-keypair p256`.
const (
	concealedPrivateKey = "MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgKY3ewzdo/MQqOyAktuCgwbBmat+3WDmRqq4/8BMXswehRANCAATIa9uGS+yxqquxQWCK2+EOkjr15FNAcmBjmaBNCiO8p6ztuicLUh5+cvPMwIlprL0uGC42ngkdFcvpYQxGXWDp"
	concealedPublicKey  = "BMhr24ZL7LGqq7FBYIrb4Q6SOvXkU0ByYGOZoE0KI7ynrO26JwtSHn5y88zAiWmsvS4YLjaeCR0Vy-lhDEZdYOk"
)

func startConcealedServer(t *testing.T) uint16 {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	listen := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: option.Options{
			Log: &option.LogOptions{Level: "panic"},
			Endpoints: []option.Endpoint{{
				Type: C.TypeMASQUEServer,
				Tag:  "masque-in",
				Options: &option.MASQUEServerEndpointOptions{
					ListenOptions: option.ListenOptions{Listen: &listen, ListenPort: port},
					ConcealedAuth: &option.ConcealedAuthInboundOptions{
						Users: []option.ConcealedAuthUser{{KeyID: "house", PublicKey: concealedPublicKey}},
					},
					Version: []int{1, 2},
					Address: []netip.Prefix{netip.MustParsePrefix("172.31.0.1/24")},
					InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{TLS: &option.InboundTLSOptions{
						Enabled:     true,
						Certificate: []string{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))},
						Key:         []string{string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))},
					}},
				},
			}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, instance.Start())
	t.Cleanup(func() {
		instance.Close()
		cancel()
	})
	return port
}

func request(t *testing.T, port uint16, authorization func(*tls.Conn) string) (*http.Response, string) {
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}})
	require.NoError(t, err)
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	authority := "127.0.0.1:" + strconv.Itoa(int(port))
	raw := "GET /.well-known/masque/ip/*/*/ HTTP/1.1\r\nHost: " + authority + "\r\nConnection: Upgrade\r\nUpgrade: connect-ip\r\nCapsule-Protocol: ?1\r\n"
	if authorization != nil {
		raw += "Authorization: " + authorization(conn) + "\r\n"
	}
	_, err = io.WriteString(conn, raw+"\r\n")
	require.NoError(t, err)
	response, err := http.ReadResponse(std_bufio.NewReader(conn), nil)
	require.NoError(t, err)
	var body []byte
	if response.StatusCode != http.StatusSwitchingProtocols {
		body, _ = io.ReadAll(response.Body)
	}
	return response, string(body)
}

func TestConcealedAuthMASQUEServer(t *testing.T) {
	port := startConcealedServer(t)
	signer, err := concealedauth.NewSigner("house", concealedPrivateKey, "")
	require.NoError(t, err)
	prove := func(conn *tls.Conn) string {
		state := conn.ConnectionState()
		header, signErr := signer.Authorization(state.ExportKeyingMaterial, concealedauth.Origin{Scheme: "https", Host: "127.0.0.1", Port: port})
		require.NoError(t, signErr)
		return header
	}

	response, body := request(t, port, nil)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	require.Equal(t, "Not Found\n", body)
	require.Empty(t, response.Header.Get("WWW-Authenticate"))

	response, _ = request(t, port, prove)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	require.Equal(t, "connect-ip", response.Header.Get("Upgrade"))
}
