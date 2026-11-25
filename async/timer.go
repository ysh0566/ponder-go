package async

import (
	"slices"
	"sort"
	"time"
)

// Timer schedules a callback as a loop turn. The clock only wakes the loop;
// the driver runs expired callbacks in deadline order.
type Timer struct {
	loop *Loop
	at   time.Time
	wake *time.Timer
	fn   func()
}

// AfterFunc enqueues fn after d. Like other work, its callback runs when Run or
// Await drives the loop. Equal deadlines preserve registration order.
func (l *Loop) AfterFunc(d time.Duration, fn func()) *Timer {
	at := time.Now().Add(d)
	l.mu.Lock()
	defer l.mu.Unlock()
	timer := &Timer{loop: l, at: at, fn: fn}
	i := sort.Search(len(l.timers), func(i int) bool { return l.timers[i].at.After(at) })
	l.timers = slices.Insert(l.timers, i, timer)
	timer.wake = time.AfterFunc(time.Until(at), l.signal)
	return timer
}

// Stop removes a callback that has not yet been enqueued.
func (t *Timer) Stop() bool {
	l := t.loop
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.fn == nil {
		return false
	}
	i := slices.Index(l.timers, t)
	l.timers = slices.Delete(l.timers, i, i+1)
	t.fn = nil
	t.wake.Stop()
	return true
}

// enqueueTimers runs with the loop mutex held. A driver returning after a pause
// can observe multiple elapsed deadlines in a single turn.
func (l *Loop) enqueueTimers() {
	if len(l.timers) == 0 {
		return
	}
	now := time.Now()
	for len(l.timers) > 0 {
		timer := l.timers[0]
		if timer.at.After(now) {
			return
		}
		l.turns = append(l.turns, timer.fn)
		timer.fn = nil
		timer.wake.Stop()
		l.timers[0] = nil
		l.timers = l.timers[1:]
	}
}
