//go:build !go1.27 || http2legacy

package http

// golang.org/x/net/http2's own server sets Request.TLS only when :scheme is
// https.
const http2TLSFollowsScheme = true
