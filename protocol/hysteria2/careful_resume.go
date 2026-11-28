package hysteria2

import (
	"context"
	"os"
	"time"

	"github.com/pasadizo-net/pasadizo/option"
	congestion "github.com/sagernet/sing-quic/congestion_meta2"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service/filemanager"
)

// newCarefulResumeStore makes the store of Careful Resume (RFC 9959) that the
// options ask for, or returns nil when Careful Resume is off.
func newCarefulResumeStore(ctx context.Context, options *option.Hysteria2CarefulResumeOptions) (*congestion.CarefulResumeStore, error) {
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
	store, err := congestion.NewCarefulResumeStore(time.Duration(options.Lifetime), stateFile)
	if err != nil {
		return nil, E.Cause(err, "careful_resume: read state file")
	}
	return store, nil
}
