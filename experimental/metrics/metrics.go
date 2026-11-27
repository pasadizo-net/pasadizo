// Package metrics writes the Prometheus metrics that the Clash API and the
// standalone metrics listener serve.
package metrics

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	boxMetrics "github.com/pasadizo-net/pasadizo/common/metrics"
	"github.com/pasadizo-net/pasadizo/common/trafficcontrol"
	"github.com/pasadizo-net/pasadizo/common/urltest"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

// ContentType is the Prometheus text exposition format, version 0.0.4.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// processStart stands in for the start time of the process: the package is
// initialized before main runs.
var processStart = time.Now()

// Collector gathers the metrics of a box.
type Collector struct {
	registry       *boxMetrics.Registry
	trafficManager *trafficcontrol.Manager
	urlTestHistory *urltest.HistoryStorage
	outbound       adapter.OutboundManager
	endpoint       adapter.EndpointManager
	sources        bool
}

// NewCollector reads the services it needs from the box context. The traffic
// manager must be registered.
func NewCollector(ctx context.Context) *Collector {
	return &Collector{
		registry:       boxMetrics.FromContext(ctx),
		trafficManager: service.PtrFromContext[trafficcontrol.Manager](ctx),
		urlTestHistory: service.PtrFromContext[urltest.HistoryStorage](ctx),
		outbound:       service.FromContext[adapter.OutboundManager](ctx),
		endpoint:       service.FromContext[adapter.EndpointManager](ctx),
	}
}

type metricsSnapshot struct {
	StartTime   time.Time
	Counters    boxMetrics.Snapshot
	Connections int
	Upload      int64
	Download    int64
	Memory      uint64
	Outbounds   []outboundMetrics
	Groups      []groupMetrics
	// Sources is nil unless the collector writes per-source metrics.
	Sources []trafficcontrol.SourceStatistics
}

type outboundMetrics struct {
	Tag         string
	Upload      int64
	Download    int64
	Connections int
	Delay       *adapter.URLTestHistory
}

type groupMetrics struct {
	Tag      string
	Selected string
	All      []string
}

// WithSources makes the collector also write the traffic and the open
// connections of each source address, per inbound. One series per client
// address: turn it on only where the clients are known.
func (c *Collector) WithSources(enabled bool) *Collector {
	c.sources = enabled
	return c
}

// ServeHTTP writes the metrics in the Prometheus text format.
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var buffer bytes.Buffer
	c.Write(&buffer)
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(buffer.Bytes())
	}
}

// Write writes the metrics in the Prometheus text format.
func (c *Collector) Write(writer io.Writer) {
	writeMetrics(writer, c.snapshot())
}

func inuseMemory() uint64 {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	return memStats.StackInuse + memStats.HeapInuse + memStats.HeapIdle - memStats.HeapReleased
}

func (c *Collector) snapshot() *metricsSnapshot {
	upload, download := c.trafficManager.Total()
	snapshot := &metricsSnapshot{
		StartTime:   processStart,
		Counters:    c.registry.Snapshot(),
		Connections: c.trafficManager.ConnectionsLen(),
		Upload:      upload,
		Download:    download,
		Memory:      inuseMemory(),
	}
	outboundTotal := c.trafficManager.OutboundTotal()
	managedOutbounds := c.outbound.Outbounds()
	endpoints := c.endpoint.Endpoints()
	outbounds := make([]adapter.Outbound, 0, len(managedOutbounds)+len(endpoints))
	outbounds = append(outbounds, managedOutbounds...)
	for _, endpoint := range endpoints {
		outbounds = append(outbounds, endpoint)
	}
	for _, detour := range outbounds {
		tag := detour.Tag()
		if tag == "" {
			continue
		}
		if outboundGroup, isGroup := detour.(adapter.OutboundGroup); isGroup {
			groupMetric := groupMetrics{
				Tag: tag,
				All: outboundGroup.All(),
			}
			if selected := outboundGroup.Selected(N.NetworkTCP); selected != nil {
				groupMetric.Selected = selected.Tag()
			}
			snapshot.Groups = append(snapshot.Groups, groupMetric)
			continue
		}
		statistics := outboundTotal[tag]
		delete(outboundTotal, tag)
		snapshot.Outbounds = append(snapshot.Outbounds, outboundMetrics{
			Tag:         tag,
			Upload:      statistics.Upload,
			Download:    statistics.Download,
			Connections: statistics.Connections,
			Delay:       c.urlTestHistory.LoadURLTestHistory(tag),
		})
	}
	// Traffic of outbounds that are gone or have no tag.
	for tag, statistics := range outboundTotal {
		snapshot.Outbounds = append(snapshot.Outbounds, outboundMetrics{
			Tag:         tag,
			Upload:      statistics.Upload,
			Download:    statistics.Download,
			Connections: statistics.Connections,
		})
	}
	if c.sources {
		snapshot.Sources = c.trafficManager.SourceStatistics(true)
	}
	sort.Slice(snapshot.Outbounds, func(i, j int) bool {
		return snapshot.Outbounds[i].Tag < snapshot.Outbounds[j].Tag
	})
	sort.Slice(snapshot.Groups, func(i, j int) bool {
		return snapshot.Groups[i].Tag < snapshot.Groups[j].Tag
	})
	return snapshot
}

func writeMetrics(writer io.Writer, snapshot *metricsSnapshot) {
	w := &metricsWriter{writer: writer}
	w.header("sing_box_connections", "gauge", "Number of open connections.")
	w.sample("sing_box_connections", nil, strconv.Itoa(snapshot.Connections))
	w.header("sing_box_upload_bytes_total", "counter", "Bytes sent through all outbounds.")
	w.sample("sing_box_upload_bytes_total", nil, strconv.FormatInt(snapshot.Upload, 10))
	w.header("sing_box_download_bytes_total", "counter", "Bytes received through all outbounds.")
	w.sample("sing_box_download_bytes_total", nil, strconv.FormatInt(snapshot.Download, 10))
	w.header("sing_box_memory_inuse_bytes", "gauge", "Memory in use by the Go runtime.")
	w.sample("sing_box_memory_inuse_bytes", nil, strconv.FormatUint(snapshot.Memory, 10))

	w.header("sing_box_outbound_connections", "gauge", "Number of open connections per outbound.")
	for _, outbound := range snapshot.Outbounds {
		w.sample("sing_box_outbound_connections", []string{"outbound", outbound.Tag}, strconv.Itoa(outbound.Connections))
	}
	w.header("sing_box_outbound_upload_bytes_total", "counter", "Bytes sent per outbound.")
	for _, outbound := range snapshot.Outbounds {
		w.sample("sing_box_outbound_upload_bytes_total", []string{"outbound", outbound.Tag}, strconv.FormatInt(outbound.Upload, 10))
	}
	w.header("sing_box_outbound_download_bytes_total", "counter", "Bytes received per outbound.")
	for _, outbound := range snapshot.Outbounds {
		w.sample("sing_box_outbound_download_bytes_total", []string{"outbound", outbound.Tag}, strconv.FormatInt(outbound.Download, 10))
	}
	w.header("sing_box_outbound_delay_milliseconds", "gauge", "Delay of the last successful URL test per outbound.")
	for _, outbound := range snapshot.Outbounds {
		if outbound.Delay != nil {
			w.sample("sing_box_outbound_delay_milliseconds", []string{"outbound", outbound.Tag}, strconv.FormatUint(uint64(outbound.Delay.Delay), 10))
		}
	}
	w.header("sing_box_outbound_delay_timestamp_seconds", "gauge", "Unix time of the last successful URL test per outbound.")
	for _, outbound := range snapshot.Outbounds {
		if outbound.Delay != nil {
			w.sample("sing_box_outbound_delay_timestamp_seconds", []string{"outbound", outbound.Tag}, strconv.FormatFloat(float64(outbound.Delay.Time.UnixMilli())/1000, 'f', -1, 64))
		}
	}
	w.header("sing_box_group_selected", "gauge", "Whether the member is the selected outbound of the group (1) or not (0).")
	for _, group := range snapshot.Groups {
		for _, member := range group.All {
			value := "0"
			if member == group.Selected {
				value = "1"
			}
			w.sample("sing_box_group_selected", []string{"group", group.Tag, "outbound", member}, value)
		}
	}

	if snapshot.Sources != nil {
		w.header("sing_box_source_connections", "gauge", "Number of open connections per source address and inbound.")
		for _, source := range snapshot.Sources {
			w.sample("sing_box_source_connections", sourceLabels(source), strconv.Itoa(source.Connections))
		}
		w.header("sing_box_source_upload_bytes_total", "counter", "Bytes sent per source address and inbound.")
		for _, source := range snapshot.Sources {
			w.sample("sing_box_source_upload_bytes_total", sourceLabels(source), strconv.FormatInt(source.Upload, 10))
		}
		w.header("sing_box_source_download_bytes_total", "counter", "Bytes received per source address and inbound.")
		for _, source := range snapshot.Sources {
			w.sample("sing_box_source_download_bytes_total", sourceLabels(source), strconv.FormatInt(source.Download, 10))
		}
	}

	counters := snapshot.Counters
	w.header("sing_box_inbound_connections_total", "counter", "Connections accepted per inbound.")
	for _, inbound := range counters.Inbounds {
		w.sample("sing_box_inbound_connections_total", []string{"inbound", inbound.Inbound, "network", inbound.Network}, strconv.FormatUint(inbound.Connections, 10))
	}
	w.header("sing_box_inbound_errors_total", "counter", "Inbound connections that failed to route or to dial their outbound.")
	for _, inbound := range counters.InboundErrors {
		w.sample("sing_box_inbound_errors_total", []string{"inbound", inbound.Inbound}, strconv.FormatUint(inbound.Errors, 10))
	}
	w.header("sing_box_outbound_dials_total", "counter", "Dials through each outbound.")
	for _, dial := range counters.Dials {
		w.sample("sing_box_outbound_dials_total", []string{"outbound", dial.Outbound}, strconv.FormatUint(dial.Dials, 10))
	}
	w.header("sing_box_outbound_dial_errors_total", "counter", "Failed dials through each outbound.")
	for _, dial := range counters.Dials {
		w.sample("sing_box_outbound_dial_errors_total", []string{"outbound", dial.Outbound}, strconv.FormatUint(dial.Errors, 10))
	}
	w.header("sing_box_outbound_dial_duration_seconds", "histogram", "Duration of dials through each outbound, failed ones included.")
	for _, dial := range counters.Dials {
		for i, bound := range boxMetrics.DialDurationBuckets {
			w.sample("sing_box_outbound_dial_duration_seconds_bucket", []string{"outbound", dial.Outbound, "le", strconv.FormatFloat(bound, 'f', -1, 64)}, strconv.FormatUint(dial.Buckets[i], 10))
		}
		count := strconv.FormatUint(dial.Buckets[len(dial.Buckets)-1], 10)
		w.sample("sing_box_outbound_dial_duration_seconds_bucket", []string{"outbound", dial.Outbound, "le", "+Inf"}, count)
		w.sample("sing_box_outbound_dial_duration_seconds_sum", []string{"outbound", dial.Outbound}, strconv.FormatFloat(dial.Sum, 'f', -1, 64))
		w.sample("sing_box_outbound_dial_duration_seconds_count", []string{"outbound", dial.Outbound}, count)
	}
	w.header("process_start_time_seconds", "gauge", "Start time of the process since unix epoch in seconds.")
	w.sample("process_start_time_seconds", nil, strconv.FormatFloat(float64(snapshot.StartTime.UnixMilli())/1000, 'f', -1, 64))
}

func sourceLabels(source trafficcontrol.SourceStatistics) []string {
	return []string{"source", source.Source.String(), "inbound", source.Inbound}
}

type metricsWriter struct {
	writer io.Writer
}

func (w *metricsWriter) header(name string, metricType string, help string) {
	_, _ = io.WriteString(w.writer, "# HELP "+name+" "+metricsHelpEscaper.Replace(help)+"\n# TYPE "+name+" "+metricType+"\n")
}

// sample writes one sample; labels holds name and value pairs.
func (w *metricsWriter) sample(name string, labels []string, value string) {
	var builder strings.Builder
	builder.WriteString(name)
	if len(labels) > 0 {
		builder.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(labels[i])
			builder.WriteString(`="`)
			builder.WriteString(metricsLabelEscaper.Replace(labels[i+1]))
			builder.WriteByte('"')
		}
		builder.WriteByte('}')
	}
	builder.WriteByte(' ')
	builder.WriteString(value)
	builder.WriteByte('\n')
	_, _ = io.WriteString(w.writer, builder.String())
}

var (
	metricsHelpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	metricsLabelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)
