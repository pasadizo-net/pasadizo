package option

import (
	"net/netip"

	"github.com/sagernet/sing/common/json/badoption"
)

// ConcealedAuthInboundOptions turns on the Concealed HTTP authentication
// scheme of RFC 9729 on an HTTP server.
type ConcealedAuthInboundOptions struct {
	Users                []ConcealedAuthUser              `json:"users,omitempty"`
	Fallback             *ServerOptions                   `json:"fallback,omitempty"`
	FrontendSourceIPCIDR badoption.Listable[netip.Prefix] `json:"frontend_source_ip_cidr,omitempty"`
}

type ConcealedAuthUser struct {
	Name      string `json:"name,omitempty"`
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

// ConcealedAuthOutboundOptions makes an HTTP client prove a key with the
// Concealed HTTP authentication scheme of RFC 9729.
type ConcealedAuthOutboundOptions struct {
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"`
	Realm      string `json:"realm,omitempty"`
}
