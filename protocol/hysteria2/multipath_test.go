package hysteria2

import (
	"context"
	"testing"

	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing-quic/hysteria"
	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func TestNewMultipathPathsNeedsChromeParrotDisabled(t *testing.T) {
	t.Parallel()
	_, err := newMultipathPaths(context.Background(), option.Hysteria2OutboundOptions{
		Multipath: &option.Hysteria2OutboundMultipath{Paths: []option.Hysteria2MultipathPath{{}}},
	}, 1)
	require.ErrorContains(t, err, "disable_chrome_parrot")
}

func TestNewMultipathPathsNone(t *testing.T) {
	t.Parallel()
	paths, err := newMultipathPaths(context.Background(), option.Hysteria2OutboundOptions{}, 1)
	require.NoError(t, err)
	require.Empty(t, paths)
}

func TestMultipathOptionsJSON(t *testing.T) {
	t.Parallel()
	var options option.Hysteria2OutboundOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"server": "192.0.2.1",
		"disable_chrome_parrot": true,
		"multipath": {"paths": [{"bind_interface": "lte0", "backup": true, "up_mbps": 10}]}
	}`), &options))
	require.Len(t, options.Multipath.Paths, 1)
	path := options.Multipath.Paths[0]
	require.Equal(t, "lte0", path.BindInterface)
	require.True(t, path.Backup)
	require.Equal(t, 10, path.UpMbps)

	paths, err := newMultipathPaths(context.Background(), options, 2)
	require.NoError(t, err)
	require.Len(t, paths, 1)
	require.Equal(t, "0 (lte0)", paths[0].Name)
	require.True(t, paths[0].Backup)
	require.Equal(t, uint64(5*hysteria.MbpsToBps), paths[0].SendBPS)
	require.NotNil(t, paths[0].Dialer)
}
