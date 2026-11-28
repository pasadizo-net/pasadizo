package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/service"
	"github.com/pasadizo-net/pasadizo/service/oomkiller"
)

func registerOOMKillerService(registry *service.Registry) {
	oomkiller.RegisterService(registry)
}
