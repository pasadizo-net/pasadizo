//go:build !with_quic

package gost

import (
	"context"

	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"
)

func (h *Outbound) icmpSessionDialer(ctx context.Context, options option.GostOutboundOptions) (func(ctx context.Context) (session, error), error) {
	return nil, C.ErrQUICNotIncluded
}
