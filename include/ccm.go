//go:build with_ccm && (!darwin || cgo)

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/service"
	"github.com/pasadizo-net/pasadizo/service/ccm"
)

func registerCCMService(registry *service.Registry) {
	ccm.RegisterService(registry)
}
