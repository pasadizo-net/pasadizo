//go:build with_wireguard

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/endpoint"
	"github.com/pasadizo-net/pasadizo/protocol/wireguard"
)

func registerWireGuardEndpoint(registry *endpoint.Registry) {
	wireguard.RegisterEndpoint(registry)
}
