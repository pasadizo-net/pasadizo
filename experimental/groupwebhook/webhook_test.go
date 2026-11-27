package groupwebhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

type testTransport struct {
	*http.Transport
}

func (t *testTransport) Reset() {
	t.CloseIdleConnections()
}

type testHTTPClientManager struct {
	transport adapter.HTTPTransport
}

func (m *testHTTPClientManager) ResolveTransport(context.Context, logger.ContextLogger, option.HTTPClientOptions) (adapter.HTTPTransport, error) {
	return m.transport, nil
}

func (m *testHTTPClientManager) DefaultTransport() adapter.HTTPTransport {
	return m.transport
}

func (m *testHTTPClientManager) ResetNetwork() {}

func startTestWebhook(t *testing.T, options option.GroupWebhookOptions) *Webhook {
	t.Helper()
	ctx := service.ContextWith[adapter.HTTPClientManager](context.Background(), &testHTTPClientManager{
		transport: &testTransport{Transport: &http.Transport{}},
	})
	webhook, err := New(ctx, logger.NOP(), options)
	require.NoError(t, err)
	webhook.retryDelay = 10 * time.Millisecond
	scope := adapter.NewScope(ctx, logger.NOP())
	require.NoError(t, webhook.Start(adapter.StartStatePostStart, scope))
	t.Cleanup(func() {
		require.NoError(t, scope.Close())
	})
	return webhook
}

func testEvent() adapter.OutboundGroupEvent {
	return adapter.OutboundGroupEvent{
		Type:   adapter.OutboundGroupEventSelected,
		Group:  "auto",
		From:   "a",
		To:     "b",
		Reason: adapter.OutboundGroupEventReasonURLTest,
		Time:   time.Unix(1700000000, 0).UTC(),
	}
}

func TestWebhookDeliver(t *testing.T) {
	t.Parallel()
	received := make(chan *http.Request, 1)
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		received <- request
		bodies <- body
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	webhook := startTestWebhook(t, option.GroupWebhookOptions{
		URL: server.URL + "/hook",
		Headers: badoption.HTTPHeader{
			"Authorization": {"Bearer token"},
		},
	})
	webhook.NotifyOutboundGroupEvent(testEvent())
	request := <-received
	require.Equal(t, http.MethodPost, request.Method)
	require.Equal(t, "/hook", request.URL.Path)
	require.Equal(t, "application/json", request.Header.Get("Content-Type"))
	require.Equal(t, "Bearer token", request.Header.Get("Authorization"))
	require.Equal(t, map[string]any{
		"type":   "selected",
		"group":  "auto",
		"from":   "a",
		"to":     "b",
		"reason": "url_test",
		"time":   "2023-11-14T22:13:20Z",
	}, <-bodies)
}

func TestWebhookRetry(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	delivered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) < 3 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		close(delivered)
	}))
	t.Cleanup(server.Close)
	webhook := startTestWebhook(t, option.GroupWebhookOptions{URL: server.URL})
	webhook.NotifyOutboundGroupEvent(testEvent())
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered after retries")
	}
	require.Equal(t, int32(3), requests.Load())
}

func TestWebhookNoRetryOnClientError(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	webhook := startTestWebhook(t, option.GroupWebhookOptions{URL: server.URL})
	webhook.NotifyOutboundGroupEvent(testEvent())
	require.Eventually(t, func() bool {
		return requests.Load() == 1
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, int32(1), requests.Load())
}

func TestWebhookNotifyDoesNotBlock(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		close(release)
	})
	webhook := startTestWebhook(t, option.GroupWebhookOptions{
		URL:         server.URL,
		Timeout:     badoption.Duration(time.Minute),
		MaxAttempts: 1,
		QueueSize:   1,
	})
	done := make(chan struct{})
	go func() {
		for range 10 {
			webhook.NotifyOutboundGroupEvent(testEvent())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notify blocked on a stalled endpoint")
	}
}

func TestWebhookInvalidURL(t *testing.T) {
	t.Parallel()
	_, err := New(context.Background(), logger.NOP(), option.GroupWebhookOptions{})
	require.Error(t, err)
	_, err = New(context.Background(), logger.NOP(), option.GroupWebhookOptions{URL: "ftp://127.0.0.1/"})
	require.Error(t, err)
}

func TestWebhookSignsWithSecret(t *testing.T) {
	t.Parallel()
	type received struct {
		timestamp string
		signature string
		body      []byte
	}
	requests := make(chan received, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests <- received{request.Header.Get(TimestampHeader), request.Header.Get(SignatureHeader), body}
	}))
	t.Cleanup(server.Close)
	webhook := startTestWebhook(t, option.GroupWebhookOptions{URL: server.URL, Secret: "s3cret"})
	webhook.NotifyOutboundGroupEvent(testEvent())
	select {
	case request := <-requests:
		require.NotEmpty(t, request.timestamp)
		seconds, err := strconv.ParseInt(request.timestamp, 10, 64)
		require.NoError(t, err)
		require.InDelta(t, time.Now().Unix(), seconds, 5)
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write([]byte(request.timestamp + "."))
		mac.Write(request.body)
		require.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), request.signature)
		require.NotEqual(t, Sign("other", request.timestamp, request.body), request.signature)
	case <-time.After(5 * time.Second):
		t.Fatal("no request")
	}
}

func TestWebhookWithoutSecretSendsNoSignature(t *testing.T) {
	t.Parallel()
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		headers <- request.Header
	}))
	t.Cleanup(server.Close)
	webhook := startTestWebhook(t, option.GroupWebhookOptions{URL: server.URL})
	webhook.NotifyOutboundGroupEvent(testEvent())
	select {
	case header := <-headers:
		require.Empty(t, header.Get(TimestampHeader))
		require.Empty(t, header.Get(SignatureHeader))
	case <-time.After(5 * time.Second):
		t.Fatal("no request")
	}
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		followed.Add(1)
	}))
	t.Cleanup(target.Close)
	redirected := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		redirected <- struct{}{}
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	headers := badoption.HTTPHeader{"X-Token": {"token"}}
	webhook := startTestWebhook(t, option.GroupWebhookOptions{URL: server.URL, Headers: headers, Secret: "s3cret", MaxAttempts: 1})
	webhook.NotifyOutboundGroupEvent(testEvent())
	select {
	case <-redirected:
	case <-time.After(5 * time.Second):
		t.Fatal("no request")
	}
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, followed.Load(), "the redirect was followed")
}
