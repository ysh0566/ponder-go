package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ysh0566/ponder-go/query"
)

// ApplicationIndexer reports whether the configured index is caught up.
// Returning nil marks finite indexing complete while HTTP continues serving.
type ApplicationIndexer interface {
	RunIndexing(context.Context, func(bool)) error
}

type Application struct {
	http   *query.HTTPServer
	cancel context.CancelFunc
	done   chan struct{}
	ready  chan struct{}
	err    error
}

// StartApplication waits for the first successful sync. The listener serves
// health and progress from the start of backfill.
func StartApplication(ctx context.Context, engine *Engine, routes *query.Server, host string, port int) (*Application, error) {
	var indexer ApplicationIndexer
	if engine != nil {
		indexer = engine
	}
	app, err := LaunchApplication(ctx, indexer, routes, host, port)
	if err != nil {
		return nil, err
	}
	select {
	case <-app.ready:
		return app, nil
	case <-app.done:
		if app.err != nil {
			return nil, app.err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("application stopped before initial sync")
	}
}

// LaunchApplication returns the bound address before historical indexing completes.
func LaunchApplication(ctx context.Context, indexer ApplicationIndexer, routes *query.Server, host string, port int) (*Application, error) {
	if routes == nil || routes.Store == nil {
		return nil, errors.New("application requires a query server with a store")
	}
	if override, ok := os.LookupEnv("PONDER_HOSTNAME"); ok {
		host = override
	}
	if raw := os.Getenv("PONDER_PORT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 65535 {
			return nil, fmt.Errorf("invalid PONDER_PORT %q", raw)
		}
		port = n
	}
	run, cancel := context.WithCancel(ctx)
	routes.Ready.Store(false)
	listener, err := query.Listen(run, host, port, routes)
	if err != nil {
		cancel()
		return nil, err
	}
	app := &Application{http: listener, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	var once sync.Once
	setReady := func(ready bool) {
		ready = ready && run.Err() == nil
		routes.Ready.Store(ready)
		if ready {
			once.Do(func() { close(app.ready) })
		}
	}
	var indexing chan error
	if indexer == nil {
		setReady(true)
	} else {
		indexing = make(chan error, 1)
		go func(out chan<- error) { out <- indexer.RunIndexing(run, setReady) }(indexing)
	}
	go func() {
		defer close(app.done)
		select {
		case <-run.Done():
		case <-listener.Done():
		case app.err = <-indexing:
			indexing = nil
			if app.err == nil && run.Err() == nil {
				setReady(true)
				select {
				case <-run.Done():
				case <-listener.Done():
				}
			}
		}
		cancel()
		setReady(false)
		shutdown, finish := context.WithTimeout(context.Background(), 3*time.Second)
		defer finish()
		httpErr := listener.Shutdown(shutdown)
		if indexing != nil {
			app.err = <-indexing
		}
		setReady(false)
		if errors.Is(app.err, context.Canceled) || ctx.Err() != nil && errors.Is(app.err, ctx.Err()) {
			app.err = nil
		}
		app.err = errors.Join(app.err, httpErr)
	}()
	return app, nil
}
func (a *Application) Addr() net.Addr { return a.http.Addr() }
func (a *Application) Wait() error    { <-a.done; return a.err }
func (a *Application) Shutdown(ctx context.Context) error {
	a.cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return a.err
	}
}
