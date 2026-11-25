package query

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

// Listen binds before returning so callers can immediately use the actual port.
// An occupied port advances to the next one. Port zero asks the OS to choose.
func Listen(ctx context.Context, host string, port int, handler http.Handler) (*HTTPServer, error) {
	if handler == nil {
		return nil, errors.New("HTTP handler required")
	}
	listener, err := listenAvailable(ctx, host, port)
	if err != nil {
		return nil, err
	}
	s := &HTTPServer{server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}, addr: listener.Addr(), done: make(chan struct{})}
	go func() {
		s.err = s.server.Serve(listener)
		if errors.Is(s.err, http.ErrServerClosed) {
			s.err = nil
		}
		close(s.done)
	}()
	return s, nil
}

func listenAvailable(ctx context.Context, host string, port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid TCP port %d", port)
	}
	var config net.ListenConfig
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l, err := config.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return l, nil
		}
		if port == 0 || port == 65535 || !errors.Is(err, syscall.EADDRINUSE) {
			return nil, err
		}
		port++
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// NextAvailablePort probes a port and releases it. Listen reserves the port
// directly when launching a server.
func NextAvailablePort(ctx context.Context, host string, port int) (int, error) {
	l, err := listenAvailable(ctx, host, port)
	if err != nil {
		return 0, err
	}
	n := l.Addr().(*net.TCPAddr).Port
	return n, l.Close()
}

type HTTPServer struct {
	server *http.Server
	addr   net.Addr
	done   chan struct{}
	err    error
}

func (s *HTTPServer) Addr() net.Addr { return s.addr }

// Done closes when the HTTP server stops serving.
func (s *HTTPServer) Done() <-chan struct{} { return s.done }

func (s *HTTPServer) Wait() error {
	<-s.done
	return s.err
}

// Shutdown releases idle keep-alive connections immediately and gives active
// requests up to one second, bounded by the caller's context.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.server.Shutdown(wait); err != nil {
		closeErr := s.server.Close()
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), closeErr)
		}
		return closeErr
	}
	return s.Wait()
}
