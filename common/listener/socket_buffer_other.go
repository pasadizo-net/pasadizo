//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd)

package listener

import (
	C "github.com/pasadizo-net/pasadizo/constant"
)

func UDPSocketBufferSize() int {
	return C.UDPSocketBufferSize
}
