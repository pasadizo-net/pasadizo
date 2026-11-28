//go:build go1.27 && !http2legacy

package http

// Since Go 1.27, golang.org/x/net/http2's server is net/http's own, which sets
// Request.TLS on every request of a TLS connection, whatever its :scheme, so
// an HTTP/2 request's TLS state no longer tells its :scheme.
const http2TLSFollowsScheme = false
