package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/ysh0566/ponder-go/schema"
)

// QueryChanges tracks committed changes in one database namespace.
type QueryChanges struct {
	mu              sync.Mutex
	subscribers     map[chan Change]struct{}
	revision, epoch uint64
	versions        map[string]uint64
	listeners       int
}

// QueryVersion returns the latest committed change affecting any dependency.
// Database notifications also cover writes from other processes.
func (s *QueryChanges) QueryVersion(tables []string) (version uint64, listening bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	version = s.epoch
	for _, t := range tables {
		version = max(version, s.versions[t])
	}
	return version, s.listeners > 0
}

type QueryNotifications struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func (n *QueryNotifications) Done() <-chan struct{} { return n.done }

func (n *QueryNotifications) Close() error { n.cancel(); <-n.done; return n.err }

// Listen relays committed live-query notifications to readers
// and SSE subscribers. Use it with the writer's live-query triggers installed.
// The returned subscription borrows the database and owns its LISTEN session.
func (s *QueryChanges) Listen(parent context.Context, backend Backend, namespace string, tables []schema.Table) (*QueryNotifications, error) {
	ctx, cancel := context.WithCancel(parent)
	channel := namespace + "_live_query"
	listener, err := Listen(ctx, backend, channel)
	if err != nil && (ctx.Err() != nil || backend == nil || backend.Dialect() != "postgres" || !PostgresConnectionInterrupted(err)) {
		cancel()
		return nil, err
	}
	all := []string{"_ponder_checkpoint"}
	names := map[string]bool{"_ponder_checkpoint": true}
	for _, table := range tables {
		all = append(all, table.Name)
		names[table.Name] = true
	}
	if listener != nil {
		s.listenerChange(1)
	}
	n := &QueryNotifications{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(n.done)
		defer cancel()
		for {
			if listener == nil {
				listener, err = reconnectQueryListener(ctx, backend, channel)
				if err != nil {
					if ctx.Err() == nil {
						n.err = err
					}
					return
				}
				s.listenerChange(1)
				// A disconnected session could have missed any commit.
				s.notify(all)
			}
			err = s.receive(ctx, listener, all, names)
			s.listenerChange(-1)
			closeErr := listener.Close()
			if ctx.Err() != nil {
				n.err = closeErr
				s.notify(all)
				return
			}
			if backend.Dialect() != "postgres" || errors.Is(err, ErrNotificationsClosed) || closeErr != nil {
				n.err = errors.Join(err, closeErr)
				s.notify(all)
				return
			}
			listener = nil
		}
	}()
	return n, nil
}

func reconnectQueryListener(ctx context.Context, backend Backend, channel string) (NotificationListener, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		listener, err := Listen(ctx, backend, channel)
		if err == nil || !PostgresConnectionInterrupted(err) {
			return listener, err
		}
	}
}

func (s *QueryChanges) listenerChange(delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners += delta
	s.revision++
	s.epoch = s.revision
}

func (s *QueryChanges) receive(ctx context.Context, listener NotificationListener, all []string, names map[string]bool) error {
	for {
		event, err := listener.Wait(ctx)
		if err != nil {
			return err
		}
		var changed []string
		if err := json.Unmarshal([]byte(event.Payload), &changed); err != nil || changed == nil {
			s.notify(all)
			continue
		}
		tables := []string{"_ponder_checkpoint"}
		for _, table := range changed {
			if names[table] && table != "_ponder_checkpoint" {
				tables = append(tables, table)
			}
		}
		s.notify(tables)
	}
}

func (s *QueryChanges) Subscribe() (chan Change, func()) {
	ch := make(chan Change, 1)
	s.mu.Lock()
	if s.subscribers == nil {
		s.subscribers = make(map[chan Change]struct{})
	}
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() { s.mu.Lock(); delete(s.subscribers, ch); s.mu.Unlock() }
}
func (s *QueryChanges) notify(tables []string) {
	if len(tables) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	if s.versions == nil {
		s.versions = make(map[string]uint64)
	}
	for _, table := range tables {
		s.versions[table] = s.revision
	}
	for ch := range s.subscribers {
		select {
		case ch <- Change{tables}:
		default:
			var old Change
			select {
			case old = <-ch:
			default:
			}
			merged := map[string]bool{}
			for _, name := range old.Tables {
				merged[name] = true
			}
			for _, name := range tables {
				merged[name] = true
			}
			all := []string{}
			for name := range merged {
				all = append(all, name)
			}
			select {
			case ch <- Change{all}:
			default:
			}
		}
	}
}
