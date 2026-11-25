package query

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestSourceHTTPServer(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			var db store.Backend
			var err error
			if dialect == "postgres" {
				url := os.Getenv("PONDER_TEST_POSTGRES")
				if url == "" {
					t.Skip("requires a test PostgreSQL instance")
				}
				db, err = store.OpenPostgres(ctx, url)
			} else {
				db, err = store.OpenPGlite(ctx, "memory://")
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			for _, name := range []string{"listens on ipv4", "listens on ipv6", "not ready", "ready", "health", "healthy PUT", "metrics", "metrics error", "metrics PUT", "metrics unmatched route", "missing route", "custom api route", "custom hono route", "kill closes idle keep-alive connections"} {
				t.Run(name, func(t *testing.T) {
					ctx := t.Context()
					ns := fmt.Sprintf("ponder_go_http_%x", time.Now().UnixNano())
					model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}
					d, err := store.NewLifecycle(db, model, ns, schema.Multichain, store.LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := d.Close(); err != nil {
							t.Error(err)
						}
					})
					if _, err = d.Migrate(ctx, store.Migration{BuildID: "server"}); err != nil {
						t.Fatal(err)
					}
					app := New(nil)
					app.Readiness = d
					mux := http.NewServeMux()
					app.API = mux
					hi := func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hi") }
					switch name {
					case "custom api route":
						mux.HandleFunc("GET /hi", hi)
					case "custom hono route":
						custom := http.NewServeMux()
						custom.HandleFunc("GET /hi", hi)
						app.API = custom
					case "metrics error":
						app.CollectMetrics = func(context.Context) (string, error) { return "", errors.New("collection failed") }
					}
					server, err := Listen(ctx, "", 42069, app)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := server.Shutdown(context.Background()); err != nil {
							t.Error(err)
						}
					})
					port := strconv.Itoa(server.Addr().(*net.TCPAddr).Port)
					base := "http://localhost:" + port
					transport := &http.Transport{}
					defer transport.CloseIdleConnections()
					client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
					request := func(method, path string, want int) string {
						t.Helper()
						req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
						if err != nil {
							t.Fatal(err)
						}
						resp, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						defer resp.Body.Close()
						body, err := io.ReadAll(resp.Body)
						if err != nil || resp.StatusCode != want {
							t.Fatalf("%s %s: %d, %q, %v", method, path, resp.StatusCode, body, err)
						}
						return string(body)
					}
					switch name {
					case "listens on ipv4":
						request("GET", "/health", 200)
						base = "http://127.0.0.1:" + port
						request("GET", "/health", 200)
					case "health":
						request("GET", "/health", 200)
					case "listens on ipv6":
						base = "http://[::1]:" + port
						request("GET", "/health", 200)
					case "not ready":
						request("GET", "/ready", 503)
					case "ready":
						err = db.Exec(ctx, "UPDATE "+schema.Quote(ns)+".\"_ponder_go_app\" SET value=jsonb_set(value,'{is_ready}',to_jsonb(1))")
						if err != nil {
							t.Fatal(err)
						}
						request("GET", "/ready", 200)
					case "healthy PUT":
						request("PUT", "/health", 404)
					case "metrics":
						request("GET", "/metrics", 200)
					case "metrics error":
						request("GET", "/metrics", 500)
					case "metrics PUT":
						request("PUT", "/metrics", 404)
					case "metrics unmatched route":
						request("GET", "/unmatched", 404)
						if strings.Contains(request("GET", "/metrics", 200), `path="/unmatched"`) {
							t.Fatal("raw unmatched route was recorded")
						}
					case "missing route":
						request("GET", "/kevin", 404)
					case "custom api route", "custom hono route":
						if got := request("GET", "/hi", 200); got != "hi" {
							t.Fatal(got)
						}
					case "kill closes idle keep-alive connections":
						request("GET", "/health", 200)
						reused := false
						trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
						req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", base+"/health", nil)
						resp, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						_, err = io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
						if err != nil || resp.StatusCode != 200 || resp.Close || !reused {
							t.Fatalf("keep-alive response %d, reused %v, close %v: %v", resp.StatusCode, reused, resp.Close, err)
						}
						start := time.Now()
						if err := server.Shutdown(ctx); err != nil {
							t.Fatal(err)
						}
						if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
							t.Fatalf("shutdown took %s", elapsed)
						}
						if resp, err := client.Get(base + "/health"); err == nil {
							resp.Body.Close()
							t.Fatal("listener still accepts requests")
						}
						again, err := net.Listen("tcp", ":"+port)
						if err != nil {
							t.Fatal(err)
						}
						again.Close()
					}
				})
			}
		})
	}
}

func TestSourceAvailablePort(t *testing.T) {
	t.Run("port", func(t *testing.T) {
		port, err := NextAvailablePort(t.Context(), "", 42069)
		if err != nil || port != 42069 {
			t.Fatalf("port %d: %v", port, err)
		}
	})
}

func TestHTTPServerBoundaries(t *testing.T) {
	t.Run("occupied port increments and releases probe", func(t *testing.T) {
		first, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer first.Close()
		port := first.Addr().(*net.TCPAddr).Port
		next, err := NextAvailablePort(t.Context(), "127.0.0.1", port)
		if err != nil || next <= port {
			t.Fatal(next, err)
		}
		l, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(next)))
		if err != nil {
			t.Fatal(err)
		}
		l.Close()
	})
	t.Run("matched route template is the only custom path label", func(t *testing.T) {
		s := New(nil)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /token/{id}", func(w http.ResponseWriter, r *http.Request) { http.Error(w, r.PathValue("id"), 409) })
		s.API = mux
		req := httptest.NewRequest("GET", "/token/unique123", nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "unique123") {
			t.Fatal(rec.Result())
		}
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		body := rec.Body.String()
		if strings.Contains(body, "unique123") || !strings.Contains(body, `path="/token/{id}",status="4XX"`) {
			t.Fatal(body)
		}
	})
	t.Run("database read failure and preflight", func(t *testing.T) {
		s := New(nil)
		s.Readiness = unavailable{}
		for _, path := range []string{"/ready", "/sql/live"} {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
			if rec.Code != 500 {
				t.Fatal(path, rec.Code)
			}
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("OPTIONS", "/hi", nil)
		req.Header.Set("Access-Control-Request-Headers", "X-Token")
		s.ServeHTTP(rec, req)
		if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Headers") != "X-Token" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatal(rec.Result())
		}
	})
}

type unavailable struct{}

func (unavailable) Ready(context.Context) (bool, error) {
	return false, errors.New("database unavailable")
}

func TestHTTPShutdownActiveRequests(t *testing.T) {
	started, finished := make(chan struct{}), make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(finished)
	})
	s, err := Listen(t.Context(), "127.0.0.1", 0, handler)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		resp, err := client.Get("http://" + s.Addr().String())
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if err = s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("active connection was not closed")
	}
	if err := <-result; err == nil {
		t.Fatal("expected client connection to close")
	}
}
