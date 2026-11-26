package clashapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/common/trafficcontrol"
)

func TestSourceStatsHandler(t *testing.T) {
	handler := statsRouter(trafficcontrol.NewManager())
	for _, testCase := range []struct {
		query  string
		status int
		body   string
	}{
		{"", http.StatusOK, `{"sources":[]}`},
		{"?group=inbound", http.StatusOK, `{"sources":[]}`},
		{"?group=outbound", http.StatusBadRequest, ""},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sources"+testCase.query, nil))
		if recorder.Code != testCase.status {
			t.Errorf("%q: got status %d, expected %d", testCase.query, recorder.Code, testCase.status)
		}
		if testCase.body != "" && strings.TrimSpace(recorder.Body.String()) != testCase.body {
			t.Errorf("%q: got body %s, expected %s", testCase.query, recorder.Body.String(), testCase.body)
		}
	}
}

func TestSourceStatisticsObject(t *testing.T) {
	var item trafficcontrol.SourceStatistics
	item.Source = netip.MustParseAddr("192.168.80.2")
	item.InboundType = "tun"
	item.Inbound = "tun-in"
	item.Upload = 1
	item.Download = 2
	item.Connections = 3
	content, err := sourceStatisticsObject(item).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"connections":3,"download":2,"inbound":"tun/tun-in","sourceIP":"192.168.80.2","upload":1}`
	if string(content) != expected {
		t.Fatalf("got %s, expected %s", content, expected)
	}
}
