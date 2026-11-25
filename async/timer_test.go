package async_test

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"ponder.local/ponder-go/async"
)

// Both timers expire while the loop has no driver. Awaiting the later timer
// must still run the queue's earlier admission turn first.
func TestFrequencyWhenLoopResumesAfterBothDeadlines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loop := async.NewLoop()
		calls := 0
		q := makeQueue(t, loop, 5, 2, false, func(unit) *async.Future[unit] {
			calls++
			return async.Resolved(loop, unit{})
		})
		for range 4 {
			q.Add(unit{})
		}
		await(t, q.Start())
		equal(t, calls, 2)
		later := delay(loop, 1010*time.Millisecond, unit{}, nil)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		await(t, later)
		equal(t, q.Size(), 0)
		equal(t, calls, 4)
	})
}

func TestLoopTimersOrderAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := async.NewLoop()
		var calls []int
		end := async.NewPromise[struct{}](l)
		l.AfterFunc(2*time.Second, func() { calls = append(calls, 3); end.Resolve(struct{}{}) })
		first := l.AfterFunc(time.Second, func() { calls = append(calls, 1) })
		cancelled := l.AfterFunc(time.Second, func() { t.Fatal("stopped timer ran") })
		l.AfterFunc(time.Second, func() { calls = append(calls, 2) })
		if !cancelled.Stop() || cancelled.Stop() {
			t.Fatal("stop should succeed once")
		}
		time.Sleep(3 * time.Second)
		await(t, end.Promise)
		if !reflect.DeepEqual(calls, []int{1, 2, 3}) || first.Stop() {
			t.Fatal(calls)
		}
	})
}
