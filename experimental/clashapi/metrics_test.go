package clashapi

import (
	"bytes"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func TestWriteMetrics(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	writeMetrics(&buffer, &metricsSnapshot{
		Connections: 3,
		Upload:      1024,
		Download:    4096,
		Memory:      65536,
		Outbounds: []outboundMetrics{
			{
				Tag:         "hy2-1",
				Upload:      1000,
				Download:    4000,
				Connections: 2,
				Delay: &adapter.URLTestHistory{
					Time:  time.UnixMilli(1764240000500),
					Delay: 85,
				},
			},
			{
				Tag:         `odd "tag"` + "\\\n",
				Upload:      24,
				Download:    96,
				Connections: 1,
			},
		},
		Groups: []groupMetrics{
			{
				Tag:      "proxy",
				Selected: "hy2-1",
				All:      []string{"hy2-1", `odd "tag"` + "\\\n"},
			},
		},
	})
	require.Equal(t, `# HELP sing_box_connections Number of open connections.
# TYPE sing_box_connections gauge
sing_box_connections 3
# HELP sing_box_upload_bytes_total Bytes sent through all outbounds.
# TYPE sing_box_upload_bytes_total counter
sing_box_upload_bytes_total 1024
# HELP sing_box_download_bytes_total Bytes received through all outbounds.
# TYPE sing_box_download_bytes_total counter
sing_box_download_bytes_total 4096
# HELP sing_box_memory_inuse_bytes Memory in use by the Go runtime.
# TYPE sing_box_memory_inuse_bytes gauge
sing_box_memory_inuse_bytes 65536
# HELP sing_box_outbound_connections Number of open connections per outbound.
# TYPE sing_box_outbound_connections gauge
sing_box_outbound_connections{outbound="hy2-1"} 2
sing_box_outbound_connections{outbound="odd \"tag\"\\\n"} 1
# HELP sing_box_outbound_upload_bytes_total Bytes sent per outbound.
# TYPE sing_box_outbound_upload_bytes_total counter
sing_box_outbound_upload_bytes_total{outbound="hy2-1"} 1000
sing_box_outbound_upload_bytes_total{outbound="odd \"tag\"\\\n"} 24
# HELP sing_box_outbound_download_bytes_total Bytes received per outbound.
# TYPE sing_box_outbound_download_bytes_total counter
sing_box_outbound_download_bytes_total{outbound="hy2-1"} 4000
sing_box_outbound_download_bytes_total{outbound="odd \"tag\"\\\n"} 96
# HELP sing_box_outbound_delay_milliseconds Delay of the last successful URL test per outbound.
# TYPE sing_box_outbound_delay_milliseconds gauge
sing_box_outbound_delay_milliseconds{outbound="hy2-1"} 85
# HELP sing_box_outbound_delay_timestamp_seconds Unix time of the last successful URL test per outbound.
# TYPE sing_box_outbound_delay_timestamp_seconds gauge
sing_box_outbound_delay_timestamp_seconds{outbound="hy2-1"} 1764240000.5
# HELP sing_box_group_selected Whether the member is the selected outbound of the group (1) or not (0).
# TYPE sing_box_group_selected gauge
sing_box_group_selected{group="proxy",outbound="hy2-1"} 1
sing_box_group_selected{group="proxy",outbound="odd \"tag\"\\\n"} 0
`, buffer.String())
}
