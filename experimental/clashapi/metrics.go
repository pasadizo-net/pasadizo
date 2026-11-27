package clashapi

import (
	"bytes"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pasadizo-net/pasadizo/adapter"
	N "github.com/sagernet/sing/common/network"
)

// Prometheus text exposition format, version 0.0.4.
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

type metricsSnapshot struct {
	Connections int
	Upload      int64
	Download    int64
	Memory      uint64
	Outbounds   []outboundMetrics
	Groups      []groupMetrics
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

func metrics(server *Server) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var buffer bytes.Buffer
		writeMetrics(&buffer, server.metricsSnapshot())
		w.Header().Set("Content-Type", metricsContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buffer.Bytes())
	}
}

func (s *Server) metricsSnapshot() *metricsSnapshot {
	upload, download := s.trafficManager.Total()
	snapshot := &metricsSnapshot{
		Connections: s.trafficManager.ConnectionsLen(),
		Upload:      upload,
		Download:    download,
		Memory:      inuseMemory(),
	}
	outboundTotal := s.trafficManager.OutboundTotal()
	managedOutbounds := s.outbound.Outbounds()
	endpoints := s.endpoint.Endpoints()
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
			Delay:       s.urlTestHistory.LoadURLTestHistory(tag),
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
