package runtime

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/ysh0566/ponder-go/query"
)

// Application serves queries and optionally indexes new blocks. Wait returns
// after the listener and the indexer stop, so the caller can close the database.
type Application struct {
	http   *query.HTTPServer
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// StartApplication completes the initial sync before advertising readiness.
// Passing a nil engine starts a query-only application over the supplied store.
func StartApplication(ctx context.Context, engine *Engine, routes *query.Server, host string, port int) (*Application, error) {
	if routes == nil || routes.Store == nil {
		return nil, errors.New("application requires a query server with a store")
	}
	if engine != nil {
		if err := engine.Sync(ctx); err != nil {
			return nil, err
		}
	}
	run, cancel := context.WithCancel(ctx)
	listener, err := query.Listen(run, host, port, routes)
	if err != nil {
		cancel()
		return nil, err
	}
	app := &Application{http: listener, cancel: cancel, done: make(chan struct{})}
	routes.Ready.Store(true)
	var indexing chan error
	if engine != nil {
		indexing = make(chan error, 1)
		go func(out chan<- error) { out <- engine.poll(run) }(indexing)
	}
	go func() {
		defer close(app.done)
		select {
		case <-run.Done():
		case <-listener.Done():
		case app.err = <-indexing:
			indexing = nil
		}
		routes.Ready.Store(false)
		cancel()
		shutdown, finish := context.WithTimeout(context.Background(), 3*time.Second)
		defer finish()
		httpErr := listener.Shutdown(shutdown)
		if indexing != nil {
			app.err = <-indexing
		}
		if errors.Is(app.err, context.Canceled) || ctx.Err() != nil && errors.Is(app.err, ctx.Err()) {
			app.err = nil
		}
		app.err = errors.Join(app.err, httpErr)
	}()
	return app, nil
}

func (a *Application) Addr() net.Addr { return a.http.Addr() }

func (a *Application) Wait() error {
	<-a.done
	return a.err
}

func (a *Application) Shutdown(ctx context.Context) error {
	a.cancel()
	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
