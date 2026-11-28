package hysteria2

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pasadizo-net/pasadizo/log"
	"github.com/pasadizo-net/pasadizo/option"
	congestion "github.com/sagernet/sing-quic/congestion_meta2"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service/filemanager"
)

// newCarefulResumeStore makes the store of Careful Resume (RFC 9959) that the
// options ask for, or returns nil when Careful Resume is off. A state file
// that cannot be read is logged as a warning and starts the store empty. The
// store does nothing in the background until its Start, so a configuration
// check, or a constructor that fails later, leaves nothing running.
func newCarefulResumeStore(ctx context.Context, logger log.ContextLogger, options *option.Hysteria2CarefulResumeOptions) (*congestion.CarefulResumeStore, error) {
	if options == nil || !options.Enabled {
		return nil, nil
	}
	if options.Lifetime < 0 {
		return nil, E.New("careful_resume: negative lifetime")
	}
	var stateFile string
	if options.StateFile != "" {
		stateFile = filemanager.BasePath(ctx, os.ExpandEnv(options.StateFile))
	}
	return congestion.NewCarefulResumeStore(time.Duration(options.Lifetime), stateFile, func(format string, a ...any) {
		logger.Warn(fmt.Sprintf(format, a...))
	}), nil
}
