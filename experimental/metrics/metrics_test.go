package metrics

import (
	"bytes"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	boxMetrics "github.com/pasadizo-net/pasadizo/common/metrics"

	"github.com/stretchr/testify/require"
)

func TestWriteMetrics(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	writeMetrics(&buffer, &metricsSnapshot{
		StartTime: time.UnixMilli(1764230000250),
		Counters: boxMetrics.Snapshot{
			Inbounds: []boxMetrics.InboundSnapshot{
				{Inbound: "in-8080", Network: "tcp", Connections: 120},
				{Inbound: "in-8080", Network: "udp", Connections: 4},
			},
			InboundErrors: []boxMetrics.InboundErrorSnapshot{
				{Inbound: "in-8080", Errors: 7},
			},
			Dials: []boxMetrics.DialSnapshot{
				{
					Outbound: "hy2-1",
					Dials:    10,
					Errors:   2,
					Buckets:  []uint64{1, 3, 6, 8, 8, 9, 9, 9, 10, 10},
					Sum:      12.5,
				},
			},
		},
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
# HELP sing_box_inbound_connections_total Connections accepted per inbound.
# TYPE sing_box_inbound_connections_total counter
sing_box_inbound_connections_total{inbound="in-8080",network="tcp"} 120
sing_box_inbound_connections_total{inbound="in-8080",network="udp"} 4
# HELP sing_box_inbound_errors_total Inbound connections that failed to route or to dial their outbound.
# TYPE sing_box_inbound_errors_total counter
sing_box_inbound_errors_total{inbound="in-8080"} 7
# HELP sing_box_outbound_dials_total Dials through each outbound.
# TYPE sing_box_outbound_dials_total counter
sing_box_outbound_dials_total{outbound="hy2-1"} 10
# HELP sing_box_outbound_dial_errors_total Failed dials through each outbound.
# TYPE sing_box_outbound_dial_errors_total counter
sing_box_outbound_dial_errors_total{outbound="hy2-1"} 2
# HELP sing_box_outbound_dial_duration_seconds Duration of dials through each outbound, failed ones included.
# TYPE sing_box_outbound_dial_duration_seconds histogram
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="0.05"} 1
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="0.1"} 3
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="0.25"} 6
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="0.5"} 8
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="1"} 8
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="2.5"} 9
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="5"} 9
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="10"} 9
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="30"} 10
sing_box_outbound_dial_duration_seconds_bucket{outbound="hy2-1",le="+Inf"} 10
sing_box_outbound_dial_duration_seconds_sum{outbound="hy2-1"} 12.5
sing_box_outbound_dial_duration_seconds_count{outbound="hy2-1"} 10
# HELP process_start_time_seconds Start time of the process since unix epoch in seconds.
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1764230000.25
`, buffer.String())
}
