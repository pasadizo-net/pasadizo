package http

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/pasadizo-net/pasadizo/common/concealedauth"
	"github.com/pasadizo-net/pasadizo/common/tls"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// ConcealedAuthOptions turn on the Concealed HTTP authentication scheme of
// RFC 9729. A request without a valid proof gets the decoy: the fallback's
// answer when Fallback is set, and a plain 404 otherwise.
type ConcealedAuthOptions struct {
	Verifier *concealedauth.Verifier
	// Fallback is a plain HTTP server that answers every request without a proof.
	Fallback M.Socksaddr
	// FrontendSourceIPCIDR are the sources trusted to send Concealed-Auth-Export.
	FrontendSourceIPCIDR []netip.Prefix
}

// NewConcealedAuthOptions builds ConcealedAuthOptions from the configuration,
// or returns nil when it is absent.
func NewConcealedAuthOptions(options *option.ConcealedAuthInboundOptions) (*ConcealedAuthOptions, error) {
	if options == nil {
		return nil, nil
	}
	if len(options.Users) == 0 {
		return nil, E.New("concealed_auth: missing users")
	}
	users := make([]concealedauth.User, 0, len(options.Users))
	for _, user := range options.Users {
		users = append(users, concealedauth.User{Name: user.Name, KeyID: user.KeyID, PublicKey: user.PublicKey})
	}
	verifier, err := concealedauth.NewVerifier(users)
	if err != nil {
		return nil, E.Cause(err, "concealed_auth")
	}
	concealedOptions := &ConcealedAuthOptions{
		Verifier:             verifier,
		FrontendSourceIPCIDR: options.FrontendSourceIPCIDR,
	}
	if options.Fallback != nil && options.Fallback.Server != "" {
		concealedOptions.Fallback = options.Fallback.Build()
	}
	return concealedOptions, nil
}

// NewConcealedAuthSigner builds the client's signer from the configuration, or
// returns nil when it is absent.
func NewConcealedAuthSigner(options *option.ConcealedAuthOutboundOptions, tlsEnabled bool, username string) (*concealedauth.Signer, error) {
	if options == nil {
		return nil, nil
	}
	if !tlsEnabled {
		return nil, E.New("concealed_auth requires TLS")
	}
	if username != "" {
		return nil, E.New("concealed_auth and username are mutually exclusive")
	}
	signer, err := concealedauth.NewSigner(options.KeyID, options.PrivateKey, options.Realm)
	if err != nil {
		return nil, E.Cause(err, "concealed_auth")
	}
	return signer, nil
}

// connExporter exports keying material from the TLS connection under conn.
func connExporter(conn net.Conn) concealedauth.Exporter {
	return func(label string, context []byte, length int) ([]byte, error) {
		return tls.ExportKeyingMaterial(conn, label, context, length)
	}
}

// requestOrigin returns the scheme, host and port of the request's target URI
// (RFC 9729, Section 3.1). The scheme is the one of an absolute-form target and
// https otherwise, because the scheme needs TLS; the authority is the target
// of a CONNECT or absolute-form request, else the Host header or :authority.
func requestOrigin(request *http.Request) (concealedauth.Origin, bool) {
	scheme := strings.ToLower(request.URL.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	authority := request.URL.Host
	if authority == "" {
		authority = request.Host
	}
	return authorityOrigin(scheme, authority)
}

func authorityOrigin(scheme string, authority string) (concealedauth.Origin, bool) {
	origin := concealedauth.Origin{Scheme: scheme, Host: authority}
	switch scheme {
	case "http", "ws":
		origin.Port = 80
	default:
		origin.Port = 443
	}
	if index := strings.LastIndexByte(authority, ':'); index >= 0 && index > strings.LastIndexByte(authority, ']') {
		origin.Host = authority[:index]
		if portString := authority[index+1:]; portString != "" {
			port, err := strconv.ParseUint(portString, 10, 16)
			if err != nil {
				return concealedauth.Origin{}, false
			}
			origin.Port = uint16(port)
		}
	}
	return origin, origin.Host != ""
}

// authenticateConcealed verifies a Concealed header field value. A trusted
// frontend's Concealed-Auth-Export replaces the connection's own exporter
// (RFC 9729, Section 6.2); from anyone else the header is ignored.
func (s *Server) authenticateConcealed(request *http.Request, credentials *concealedauth.Credentials, source M.Socksaddr, exporter concealedauth.Exporter) (string, error) {
	origin, valid := requestOrigin(request)
	if !valid {
		return "", E.New("invalid request authority")
	}
	if s.trustedFrontend(source) {
		if output, loaded := concealedauth.ParseExportHeader(request.Header.Get(concealedauth.ExportHeader)); loaded {
			exporter = func(string, []byte, int) ([]byte, error) {
				return output, nil
			}
		}
	}
	if exporter == nil {
		return "", E.New("not a TLS connection")
	}
	return s.concealed.Verifier.Verify(credentials, origin, exporter)
}

func (s *Server) trustedFrontend(source M.Socksaddr) bool {
	if !source.IsIP() {
		return false
	}
	address := source.Addr.Unmap()
	for _, prefix := range s.concealed.FrontendSourceIPCIDR {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func newFallbackProxy(fallback M.Socksaddr) *httputil.ReverseProxy {
	target := &url.URL{Scheme: "http", Host: fallback.String()}
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Host = request.In.Host
			for name := range request.Out.Header {
				if strings.HasPrefix(name, ":") {
					delete(request.Out.Header, name)
				}
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
				return N.SystemDialer.DialContext(ctx, N.NetworkTCP, fallback)
			},
			DisableCompression: true,
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
}

// serveDecoy answers a request without a valid proof over HTTP/2 or HTTP/3 the
// way the fallback, or a server without the resource, would.
func (h *httpHandler) serveDecoy(writer http.ResponseWriter, request *http.Request) {
	if h.server.fallbackProxy != nil {
		h.server.fallbackProxy.ServeHTTP(writer, request)
		return
	}
	writeNotFound(writer)
}

func writeNotFound(writer http.ResponseWriter) {
	body := http.StatusText(http.StatusNotFound) + "\n"
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
	writer.WriteHeader(http.StatusNotFound)
	io.WriteString(writer, body)
}

// serveDecoy answers a request without a valid proof over HTTP/1. With a
// fallback, the connection from this request on is relayed to it byte for
// byte, so the probe talks to the fallback itself.
func (c *serverConn) serveDecoy(request *http.Request, cause error) (requestResult, error) {
	if !c.server.concealed.Fallback.IsValid() {
		return c.reject(request, requestKeepAlive(request), http.StatusNotFound, nil, cause)
	}
	fallbackConn, err := N.SystemDialer.DialContext(c.ctx, N.NetworkTCP, c.server.concealed.Fallback)
	if err != nil {
		return c.reject(request, false, http.StatusBadGateway, nil, E.Cause(err, "dial fallback"))
	}
	_, err = fallbackConn.Write(c.rawRequest)
	if err != nil {
		fallbackConn.Close()
		return requestClose, E.Cause(err, "write fallback")
	}
	bufio.CopyConn(c.ctx, c.conn, fallbackConn)
	return requestClose, nil
}
