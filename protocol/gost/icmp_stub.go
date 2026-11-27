//go:build !with_quic

package gost

import (
	"context"

	"github.com/pasadizo-net/pasadizo/option"
	C "github.com/pasadizo-net/pasadizo/constant"
)

func (h *Outbound) icmpSessionDialer(ctx context.Context, options option.GostOutboundOptions) (func(ctx context.Context) (session, error), error) {
	return nil, C.ErrQUICNotIncluded
}
