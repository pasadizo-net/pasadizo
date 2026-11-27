package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

var stubCollector = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", ContentType)
	_, _ = io.WriteString(w, "sing_box_connections 0\n")
})

func serve(t *testing.T, handler http.Handler, method string, path string, username string, password string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	if username != "" || password != "" {
		request.SetBasicAuth(username, password)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestHandlerServesOnlyItsPath(t *testing.T) {
	t.Parallel()
	handler := newHandler(stubCollector, "/metrics", "", "")
	response := serve(t, handler, http.MethodGet, "/metrics", "", "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, ContentType, response.Header().Get("Content-Type"))
	require.Equal(t, "sing_box_connections 0\n", response.Body.String())

	// None of the Clash API endpoints exist here.
	for _, path := range []string{"/", "/metrics/", "/proxies", "/proxies/proxy", "/connections", "/configs", "/logs", "/traffic", "/version"} {
		require.Equal(t, http.StatusNotFound, serve(t, handler, http.MethodGet, path, "", "").Code, path)
	}
}

func TestHandlerCustomPath(t *testing.T) {
	t.Parallel()
	handler := newHandler(stubCollector, "/internal/prometheus", "", "")
	require.Equal(t, http.StatusOK, serve(t, handler, http.MethodGet, "/internal/prometheus", "", "").Code)
	require.Equal(t, http.StatusNotFound, serve(t, handler, http.MethodGet, "/metrics", "", "").Code)
}

func TestHandlerIsReadOnly(t *testing.T) {
	t.Parallel()
	handler := newHandler(stubCollector, "/metrics", "", "")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		response := serve(t, handler, method, "/metrics", "", "")
		require.Equal(t, http.StatusMethodNotAllowed, response.Code, method)
		require.Equal(t, "GET, HEAD", response.Header().Get("Allow"))
	}
	require.Equal(t, http.StatusOK, serve(t, handler, http.MethodHead, "/metrics", "", "").Code)
}

func TestHandlerBasicAuth(t *testing.T) {
	t.Parallel()
	handler := newHandler(stubCollector, "/metrics", "prometheus", "s3cret")
	require.Equal(t, http.StatusOK, serve(t, handler, http.MethodGet, "/metrics", "prometheus", "s3cret").Code)
	for _, credentials := range [][2]string{{"", ""}, {"prometheus", ""}, {"prometheus", "wrong"}, {"other", "s3cret"}} {
		response := serve(t, handler, http.MethodGet, "/metrics", credentials[0], credentials[1])
		require.Equal(t, http.StatusUnauthorized, response.Code, credentials)
		require.Contains(t, response.Header().Get("WWW-Authenticate"), "Basic")
		require.NotContains(t, response.Body.String(), "sing_box_")
	}
	// An unknown path is still 404, not a hint that auth is needed.
	require.Equal(t, http.StatusNotFound, serve(t, handler, http.MethodGet, "/proxies", "", "").Code)
}
