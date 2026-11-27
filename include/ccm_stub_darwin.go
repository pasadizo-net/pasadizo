//go:build with_ccm && darwin && !cgo

package include

import (
	"context"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/adapter/service"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	E "github.com/sagernet/sing/common/exceptions"
)

func registerCCMService(registry *service.Registry) {
	service.Register[option.CCMServiceOptions](registry, C.TypeCCM, func(ctx context.Context, logger log.ContextLogger, tag string, options option.CCMServiceOptions) (adapter.Service, error) {
		return nil, E.New(`CCM requires CGO on darwin, rebuild with CGO_ENABLED=1`)
	})
}
