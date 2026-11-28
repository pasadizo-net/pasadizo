//go:build with_utls

package tls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/common/ja3"

	utls "github.com/metacubex/utls"
	"github.com/stretchr/testify/require"
)

// helloRecorder keeps the first write of a client, its ClientHello record.
type helloRecorder struct {
	net.Conn
	hello []byte
}

func (c *helloRecorder) Write(p []byte) (int, error) {
	if c.hello == nil {
		c.hello = slices.Clone(p)
	}
	return c.Conn.Write(p)
}

func TestUTLSChromeExportsKeyingMaterial(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	serverConfig := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, NextProtos: []string{"h2", "http/1.1"}}
	config := &UTLSClientConfig{
		ctx:    context.Background(),
		config: &utls.Config{ServerName: "example.com", InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}},
		id:     utls.HelloChrome_Auto,
	}

	dial := func() ([]byte, []byte, []byte) {
		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, listenErr)
		defer listener.Close()
		clientRaw, dialErr := net.Dial("tcp", listener.Addr().String())
		require.NoError(t, dialErr)
		serverRaw, acceptErr := listener.Accept()
		require.NoError(t, acceptErr)
		t.Cleanup(func() {
			clientRaw.Close()
			serverRaw.Close()
		})
		clientRaw.SetDeadline(time.Now().Add(10 * time.Second))
		serverRaw.SetDeadline(time.Now().Add(10 * time.Second))
		serverExport := make(chan []byte, 1)
		go func() {
			server := tls.Server(serverRaw, serverConfig)
			if server.Handshake() != nil {
				serverExport <- nil
				return
			}
			state := server.ConnectionState()
			material, _ := state.ExportKeyingMaterial("EXPORTER-test", []byte("context"), 32)
			serverExport <- material
			// Drain the client's records (session tickets need no reply).
			buffer := make([]byte, 1024)
			for {
				if _, readErr := server.Read(buffer); readErr != nil {
					return
				}
			}
		}()
		recorder := &helloRecorder{Conn: clientRaw}
		client, clientErr := config.Client(recorder)
		require.NoError(t, clientErr)
		require.NoError(t, client.HandshakeContext(context.Background()))
		require.Equal(t, uint16(tls.VersionTLS13), client.ConnectionState().Version)
		material, exportErr := ExportKeyingMaterial(client, "EXPORTER-test", []byte("context"), 32)
		require.NoError(t, exportErr)
		return material, <-serverExport, recorder.hello
	}

	clientMaterial, serverMaterial, firstHello := dial()
	require.Equal(t, serverMaterial, clientMaterial)

	// The next connection of the same config sends the same fingerprint,
	// renegotiation_info included: the exporter fix leaves the ClientHello alone.
	_, _, secondHello := dial()
	first, err := ja3.Compute(firstHello)
	require.NoError(t, err)
	second, err := ja3.Compute(secondHello)
	require.NoError(t, err)
	// Chrome picks new GREASE values and a new extension order each time;
	// what stays is the fingerprint without them, as JA4 reads it.
	require.Equal(t, withoutGREASE(first.CipherSuites, false), withoutGREASE(second.CipherSuites, false))
	require.Equal(t, withoutGREASE(first.Extensions, true), withoutGREASE(second.Extensions, true))
	require.Equal(t, withoutGREASE(first.EllipticCurves, false), withoutGREASE(second.EllipticCurves, false))
	require.Equal(t, withoutGREASE(first.SignatureAlgorithms, false), withoutGREASE(second.SignatureAlgorithms, false))
	require.Contains(t, first.Extensions, uint16(0xff01))
	require.Contains(t, second.Extensions, uint16(0xff01))
	require.Equal(t, utls.RenegotiateNever, config.config.Renegotiation)
}

func withoutGREASE(values []uint16, sorted bool) []uint16 {
	values = slices.DeleteFunc(slices.Clone(values), func(value uint16) bool {
		return value&0x0f0f == 0x0a0a
	})
	if sorted {
		slices.Sort(values)
	}
	return values
}
