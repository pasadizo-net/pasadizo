//go:build with_ocm

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/service"
	"github.com/pasadizo-net/pasadizo/service/ocm"
)

func registerOCMService(registry *service.Registry) {
	ocm.RegisterService(registry)
}
