package settings

import (
	"context"

	"github.com/pasadizo-net/pasadizo/adapter"
)

type WIFIMonitor interface {
	ReadWIFIState(ctx context.Context) adapter.WIFIState
	Start() error
	Close() error
}
