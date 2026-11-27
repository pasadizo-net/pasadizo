//go:build with_cloudflared

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/inbound"
	"github.com/pasadizo-net/pasadizo/protocol/cloudflare"
)

func registerCloudflaredInbound(registry *inbound.Registry) {
	cloudflare.RegisterInbound(registry)
}
