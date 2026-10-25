package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"ponder.local/ponder-go/schema"
)

type Notification struct {
	Channel string `json:"channel"`
	Payload string `json:"payload"`
}

// NotificationListener receives actual database LISTEN/NOTIFY messages. Wait's
// context only cancels that wait; Listen's context controls the subscription.
// Call Close when done. PostgreSQL uses a dedicated connection; PGlite uses its
// single existing connection and multiplexes notifications from SQL responses.
type NotificationListener interface {
	Wait(context.Context) (Notification, error)
	Close() error
}

var ErrNotificationsClosed = errors.New("database notification listener is closed")

func Listen(ctx context.Context, backend Backend, channel string) (NotificationListener, error) {
	if channel == "" || len(channel) > 63 || strings.ContainsRune(channel, 0) || !utf8.ValidString(channel) {
		return nil, fmt.Errorf("notification channel must be valid UTF-8, 1..63 bytes, without NUL")
	}
	notifier, ok := backend.(interface {
		listen(context.Context, string) (NotificationListener, error)
	})
	if !ok {
		return nil, fmt.Errorf("backend does not support database notifications")
	}
	return notifier.listen(ctx, channel)
}

// A reader never blocks the database transport. As with pgx's notification
// queue, consumers are responsible for keeping up with their subscriptions.
type notificationQueue struct {
	mu      sync.Mutex
	entries []Notification
	head    int
	wake    chan struct{}
	done    chan struct{}
	err     error
}

func newNotificationQueue() *notificationQueue {
	return &notificationQueue{wake: make(chan struct{}, 1), done: make(chan struct{})}
}
func (q *notificationQueue) push(n Notification) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return
	}
	q.entries = append(q.entries, n)
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *notificationQueue) finish(e error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return
	}
	q.err = e
	close(q.done)
}
func (q *notificationQueue) Wait(ctx context.Context) (Notification, error) {
	for {
		if e := ctx.Err(); e != nil {
			return Notification{}, e
		}
		q.mu.Lock()
		if q.head < len(q.entries) {
			n := q.entries[q.head]
			q.entries[q.head] = Notification{}
			q.head++
			if q.head == len(q.entries) {
				q.entries = nil
				q.head = 0
			}
			if q.head < len(q.entries) {
				select {
				case q.wake <- struct{}{}:
				default:
				}
			}
			q.mu.Unlock()
			return n, nil
		}
		e := q.err
		q.mu.Unlock()
		if e != nil {
			return Notification{}, e
		}
		select {
		case <-ctx.Done():
			return Notification{}, ctx.Err()
		case <-q.wake:
		case <-q.done:
		}
	}
}

type pgNotifications struct {
	*notificationQueue
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *pgBackend) listen(ctx context.Context, channel string) (NotificationListener, error) {
	conn, e := pgx.Connect(ctx, p.url)
	if e != nil {
		return nil, e
	}
	closeConn := func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.Close(closeCtx)
	}
	if _, e = conn.Exec(ctx, "LISTEN "+schema.Quote(channel)); e != nil {
		closeConn()
		return nil, e
	}
	lifetime, cancel := context.WithCancel(ctx)
	l := &pgNotifications{notificationQueue: newNotificationQueue(), cancel: cancel, done: make(chan struct{})}
	p.notifyMu.Lock()
	if p.closed {
		p.notifyMu.Unlock()
		cancel()
		closeConn()
		return nil, ErrNotificationsClosed
	}
	p.listeners[l] = struct{}{}
	go func() {
		defer close(l.done)
		defer closeConn()
		defer func() { p.notifyMu.Lock(); delete(p.listeners, l); p.notifyMu.Unlock() }()
		for {
			n, e := conn.WaitForNotification(lifetime)
			if e != nil {
				l.finish(e)
				return
			}
			l.push(Notification{Channel: n.Channel, Payload: n.Payload})
		}
	}()
	p.notifyMu.Unlock()
	return l, nil
}
func (l *pgNotifications) Close() error {
	l.finish(ErrNotificationsClosed)
	l.cancel()
	<-l.done
	return nil
}

// The scanner owns stdout. SQL responses retain their request order, while
// asynchronous notification frames are dispatched independently of query locks.
func (p *pgliteBackend) readResponses(lines *bufio.Scanner) {
	defer close(p.scanDone)
	defer close(p.responses)
	for lines.Scan() {
		data := append([]byte(nil), lines.Bytes()...)
		var frame struct {
			Notification *Notification `json:"notification"`
		}
		if e := json.Unmarshal(data, &frame); e != nil {
			p.scanError = e
			break
		}
		if frame.Notification != nil {
			p.notifyMu.Lock()
			for q := range p.listeners[frame.Notification.Channel] {
				q.push(*frame.Notification)
			}
			p.notifyMu.Unlock()
		} else {
			p.responses <- data
		}
	}
	if p.scanError == nil {
		p.scanError = lines.Err()
	}
	if p.scanError == nil {
		p.scanError = io.EOF
	}
	p.notifyMu.Lock()
	for _, listeners := range p.listeners {
		for q := range listeners {
			q.finish(p.scanError)
		}
	}
	p.notifyMu.Unlock()
}

type pgliteNotifications struct {
	*notificationQueue
	p       *pgliteBackend
	channel string
	once    sync.Once
	err     error
}

func (p *pgliteBackend) listen(ctx context.Context, channel string) (NotificationListener, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrNotificationsClosed
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	l := &pgliteNotifications{notificationQueue: newNotificationQueue(), p: p, channel: channel}
	p.notifyMu.Lock()
	first := len(p.listeners[channel]) == 0
	if first {
		p.listeners[channel] = map[*notificationQueue]struct{}{}
	}
	p.listeners[channel][l.notificationQueue] = struct{}{}
	p.notifyMu.Unlock()
	if first {
		if _, e := p.roundtrip(ctx, "query", "LISTEN "+schema.Quote(channel)); e != nil {
			p.notifyMu.Lock()
			delete(p.listeners[channel], l.notificationQueue)
			p.notifyMu.Unlock()
			return nil, e
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			l.finish(ctx.Err())
			l.Close()
		case <-l.done:
		}
	}()
	return l, nil
}

func (l *pgliteNotifications) Close() error {
	l.once.Do(func() {
		l.finish(ErrNotificationsClosed)
		p := l.p
		p.mu.Lock()
		defer p.mu.Unlock()
		p.notifyMu.Lock()
		delete(p.listeners[l.channel], l.notificationQueue)
		last := len(p.listeners[l.channel]) == 0
		if last {
			delete(p.listeners, l.channel)
		}
		p.notifyMu.Unlock()
		if last && !p.closed {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, l.err = p.roundtrip(ctx, "query", "UNLISTEN "+schema.Quote(l.channel))
		}
	})
	return l.err
}
