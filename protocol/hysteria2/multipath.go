package hysteria2

import (
	"context"
	"strconv"

	"github.com/pasadizo-net/pasadizo/common/dialer"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing-quic/hysteria2"
	E "github.com/sagernet/sing/common/exceptions"
)

// newMultipathPaths creates the dialers of the paths the outbound adds to its QUIC
// connection with the multipath extension of RFC 10069.
func newMultipathPaths(ctx context.Context, options option.Hysteria2OutboundOptions, connections int) ([]hysteria2.MultipathPath, error) {
	if options.Multipath == nil || len(options.Multipath.Paths) == 0 {
		return nil, nil
	}
	if !options.DisableChromeParrot {
		return nil, E.New("multipath needs disable_chrome_parrot: the imitated Chrome handshake uses zero-length connection IDs, which the multipath extension forbids")
	}
	paths := make([]hysteria2.MultipathPath, 0, len(options.Multipath.Paths))
	for i, path := range options.Multipath.Paths {
		pathDialer, err := dialer.NewWithOptions(dialer.Options{
			Context:        ctx,
			Options:        path.DialerOptions,
			RemoteIsDomain: options.ServerIsDomain(),
		})
		if err != nil {
			return nil, E.Cause(err, "multipath path ", i)
		}
		name := strconv.Itoa(i)
		if path.BindInterface != "" {
			name += " (" + path.BindInterface + ")"
		}
		paths = append(paths, hysteria2.MultipathPath{
			Name:    name,
			Dialer:  pathDialer,
			Backup:  path.Backup,
			SendBPS: splitBandwidth(path.UpMbps, connections),
		})
	}
	return paths, nil
}
