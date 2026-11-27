package metrics

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

const DefaultPath = "/metrics"

var _ adapter.LifecycleService = (*Server)(nil)

// Server is the standalone metrics listener. It serves the Prometheus text on
// one path and nothing else, so it can be exposed to a scraper without
// exposing the Clash API.
type Server struct {
	logger     logger.ContextLogger
	listen     string
	httpServer *http.Server
}

func NewServer(ctx context.Context, logger logger.ContextLogger, options option.MetricsOptions) (*Server, error) {
	if options.Listen == "" {
		return nil, E.New("missing listen address")
	}
	path := options.Path
	if path == "" {
		path = DefaultPath
	}
	if !strings.HasPrefix(path, "/") {
		return nil, E.New("path must start with /: ", path)
	}
	if options.Password != "" && options.Username == "" {
		return nil, E.New("password without username")
	}
	return &Server{
		logger: logger,
		listen: options.Listen,
		httpServer: &http.Server{
			Handler:           newHandler(NewCollector(ctx), path, options.Username, options.Password),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

func newHandler(collector http.Handler, path string, username string, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if username != "" {
			requestUsername, requestPassword, ok := r.BasicAuth()
			usernameMatch := subtle.ConstantTimeCompare([]byte(requestUsername), []byte(username)) == 1
			passwordMatch := subtle.ConstantTimeCompare([]byte(requestPassword), []byte(password)) == 1
			if !ok || !usernameMatch || !passwordMatch {
				w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		}
		collector.ServeHTTP(w, r)
	})
}

func (s *Server) Name() string {
	return "metrics server"
}

func (s *Server) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStarted {
		return nil
	}
	listener, err := net.Listen(M.NetworkFromNetAddr(N.NetworkTCP, M.ParseSocksaddr(s.listen).Addr), s.listen)
	if err != nil {
		return E.Cause(err, "metrics listen error")
	}
	scope.Add(s.httpServer.Close)
	s.logger.Info("metrics listening at ", listener.Addr())
	go func() {
		err := s.httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("metrics serve error: ", err)
		}
	}()
	return nil
}
