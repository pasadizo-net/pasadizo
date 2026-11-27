//go:build with_usbip && (linux || (darwin && cgo) || windows)

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/service"
	"github.com/pasadizo-net/pasadizo/service/usbip"
)

func registerUSBIPServices(registry *service.Registry) {
	usbip.RegisterService(registry)
}
