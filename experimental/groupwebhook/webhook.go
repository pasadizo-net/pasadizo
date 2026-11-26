package groupwebhook

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"
)

const (
	defaultTimeout     = 5 * time.Second
	defaultMaxAttempts = 3
	defaultQueueSize   = 64
	maxRetryDelay      = 30 * time.Second
)

var (
	_ adapter.LifecycleService           = (*Webhook)(nil)
	_ adapter.OutboundGroupEventListener = (*Webhook)(nil)
)

// Webhook posts outbound group events to an HTTP endpoint.
// Events are queued and delivered in order by a single worker,
// so a slow or unreachable endpoint never blocks the groups.
type Webhook struct {
	ctx         context.Context
	cancel      context.CancelFunc
	logger      logger.ContextLogger
	options     option.GroupWebhookOptions
	link        string
	headers     http.Header
	timeout     time.Duration
	maxAttempts int
	retryDelay  time.Duration
	queue       chan adapter.OutboundGroupEvent
	done        chan struct{}
	transport   adapter.HTTPTransport
	httpClient  *http.Client
}

func New(ctx context.Context, logger logger.ContextLogger, options option.GroupWebhookOptions) (*Webhook, error) {
	if options.URL == "" {
		return nil, E.New("missing url")
	}
	linkURL, err := url.Parse(options.URL)
	if err != nil {
		return nil, E.Cause(err, "parse url")
	}
	if linkURL.Scheme != "http" && linkURL.Scheme != "https" {
		return nil, E.New("unsupported url scheme: ", linkURL.Scheme)
	}
	timeout := time.Duration(options.Timeout)
	if timeout == 0 {
		timeout = defaultTimeout
	}
	maxAttempts := int(options.MaxAttempts)
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	queueSize := int(options.QueueSize)
	if queueSize == 0 {
		queueSize = defaultQueueSize
	}
	return &Webhook{
		ctx:         ctx,
		logger:      logger,
		options:     options,
		link:        options.URL,
		headers:     options.Headers.Build(),
		timeout:     timeout,
		maxAttempts: maxAttempts,
		retryDelay:  time.Second,
		queue:       make(chan adapter.OutboundGroupEvent, queueSize),
		done:        make(chan struct{}),
	}, nil
}

func (w *Webhook) Name() string {
	return "group-webhook"
}

func (w *Webhook) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStatePostStart {
		return nil
	}
	transport, err := w.resolveTransport()
	if err != nil {
		return E.Cause(err, "create group webhook http client")
	}
	w.transport = transport
	w.httpClient = &http.Client{Transport: transport}
	w.ctx, w.cancel = context.WithCancel(w.ctx)
	go w.loop()
	scope.Add(w.close)
	return nil
}

func (w *Webhook) resolveTransport() (adapter.HTTPTransport, error) {
	httpClientManager := service.FromContext[adapter.HTTPClientManager](w.ctx)
	if httpClientManager == nil {
		return nil, E.New("missing http client manager")
	}
	if w.options.HTTPClient != nil && !w.options.HTTPClient.IsEmpty() {
		return httpClientManager.ResolveTransport(w.ctx, w.logger, *w.options.HTTPClient)
	}
	defaultTransport := httpClientManager.DefaultTransport()
	if defaultTransport == nil {
		return nil, E.New("default http client transport is not initialized")
	}
	return defaultTransport, nil
}

func (w *Webhook) close() error {
	w.cancel()
	<-w.done
	w.transport.CloseIdleConnections()
	return nil
}

func (w *Webhook) NotifyOutboundGroupEvent(event adapter.OutboundGroupEvent) {
	select {
	case w.queue <- event:
	default:
		w.logger.Warn("queue full, drop ", event.Type, " event of group ", event.Group)
	}
}

func (w *Webhook) loop() {
	defer close(w.done)
	for {
		select {
		case <-w.ctx.Done():
			return
		case event := <-w.queue:
			w.deliver(event)
		}
	}
}

func (w *Webhook) deliver(event adapter.OutboundGroupEvent) {
	content, err := json.Marshal(event)
	if err != nil {
		w.logger.Error(E.Cause(err, "encode event"))
		return
	}
	retryDelay := w.retryDelay
	for attempt := 1; ; attempt++ {
		retry, err := w.post(content)
		if err == nil {
			w.logger.Debug("delivered ", event.Type, " event of group ", event.Group)
			return
		}
		if w.ctx.Err() != nil {
			return
		}
		if !retry || attempt >= w.maxAttempts {
			w.logger.Error(E.Cause(err, "deliver ", event.Type, " event of group ", event.Group))
			return
		}
		w.logger.Debug(E.Cause(err, "deliver ", event.Type, " event of group ", event.Group, ", retry in ", retryDelay))
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(retryDelay):
		}
		retryDelay = min(retryDelay*2, maxRetryDelay)
	}
}

func (w *Webhook) post(content []byte) (retry bool, err error) {
	ctx, cancel := context.WithTimeout(w.ctx, w.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.link, bytes.NewReader(content))
	if err != nil {
		return false, err
	}
	request.Header = w.headers.Clone()
	request.Header.Set("Content-Type", "application/json")
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "sing-box/"+C.Version)
	}
	response, err := w.httpClient.Do(request)
	if err != nil {
		return true, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	_ = response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return false, nil
	}
	err = E.New("unexpected status: ", response.Status)
	switch {
	case response.StatusCode == http.StatusRequestTimeout,
		response.StatusCode == http.StatusTooManyRequests,
		response.StatusCode >= 500:
		return true, err
	default:
		return false, err
	}
}
