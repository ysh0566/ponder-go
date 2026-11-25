package query

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"
)

type Readiness interface {
	Ready(context.Context) (bool, error)
}

func (s *Server) isReady(ctx context.Context) (bool, error) {
	if s.Readiness != nil {
		return s.Readiness.Ready(ctx)
	}
	return s.Ready.Load(), nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,PUT,POST,DELETE,PATCH")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if names := r.Header.Get("Access-Control-Request-Headers"); names != "" {
			w.Header().Set("Access-Control-Allow-Headers", names)
			w.Header().Add("Vary", "Access-Control-Request-Headers")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path, handler := s.route(r)
	if path == "" || s.Metrics == nil {
		handler.ServeHTTP(w, r)
		return
	}
	finish := s.Metrics.BeginHTTP(r.Method, path)
	start := time.Now()
	status := http.StatusInternalServerError
	defer func() {
		responseLength, _ := strconv.ParseInt(w.Header().Get("Content-Length"), 10, 64)
		finish(status, r.ContentLength, responseLength, time.Since(start))
	}()
	status = httpsnoop.CaptureMetrics(handler, w, r).Code
}

func (s *Server) route(r *http.Request) (string, http.Handler) {
	switch r.URL.Path {
	case "/sql/db", "/sql/live":
		if r.Method != http.MethodGet {
			return "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			})
		}
		return r.URL.Path, http.HandlerFunc(s.serveRoute)
	case "/health", "/ready", "/metrics", "/status":
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return r.URL.Path, http.HandlerFunc(s.serveRoute)
		}
	}
	if s.API == nil {
		return "", http.NotFoundHandler()
	}
	var path string
	if mux, ok := s.API.(*http.ServeMux); ok {
		_, pattern := mux.Handler(r)
		// ServeMux patterns may include an HTTP method and host. Only a
		// registered route is suitable as a Prometheus label.
		if i := strings.IndexByte(pattern, '/'); i >= 0 {
			path = pattern[i:]
		}
	}
	return path, s.API
}
