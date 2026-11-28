package tls

import (
	"crypto/tls"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

// KeyingMaterialExporter is a connection that exports keying material itself,
// because the ConnectionState it returns cannot.
type KeyingMaterialExporter interface {
	ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error)
}

type connectionStater interface {
	ConnectionState() tls.ConnectionState
}

// ExportKeyingMaterial exports keying material (RFC 5705, RFC 8446 Section 7.5)
// from a TLS connection or a wrapper around one. Like crypto/tls, it refuses a
// TLS 1.2 session without the extended master secret.
func ExportKeyingMaterial(conn any, label string, context []byte, length int) ([]byte, error) {
	if exporter, isExporter := common.Cast[KeyingMaterialExporter](conn); isExporter {
		return exporter.ExportKeyingMaterial(label, context, length)
	}
	if stater, isStater := common.Cast[connectionStater](conn); isStater {
		state := stater.ConnectionState()
		return ExportKeyingMaterialFromState(&state, label, context, length)
	}
	return nil, E.New("not a TLS connection")
}

// ExportKeyingMaterialFromState exports keying material from a connection
// state. A state that a platform TLS engine built by hand has no exporter, and
// gives an error instead of a panic.
func ExportKeyingMaterialFromState(state *tls.ConnectionState, label string, context []byte, length int) (material []byte, err error) {
	if !state.HandshakeComplete {
		return nil, E.New("TLS handshake not complete")
	}
	defer func() {
		if recover() != nil {
			material, err = nil, E.New("keying material exporter unavailable for this TLS engine")
		}
	}()
	return state.ExportKeyingMaterial(label, context, length)
}
