package runtime

import (
	"context"
	"errors"
	"fmt"
	"ponder.local/ponder-go/rpc"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// Admission executes synchronously; completion alone runs on a worker. Unlike
// wrapping Call in a goroutine, the observer has definitely seen the request
// when BeginCall returns.
type sourceAdmissionRequester struct {
	admit  func(string, any)
	finish sourceHistoricalTestRequester
}

func (r *sourceAdmissionRequester) Call(ctx context.Context, method string, params, out any) error {
	return <-r.BeginCall(ctx, method, params, out)
}
func (r *sourceAdmissionRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	r.admit(method, params)
	result := make(chan error, 1)
	go func() { result <- r.finish(ctx, method, params, out); close(result) }()
	return result
}
func sourceAdmissionBlock(params any) filter.Object {
	number := params.([]any)[0].(string)
	return filter.Object{"hash": fmt.Sprintf("0x%064x", 1), "parentHash": fmt.Sprintf("0x%064x", 0), "number": number, "timestamp": number, "logsBloom": "0x", "transactions": []any{}}
}
func TestSourceHistoricalInitialAdmission(t *testing.T) {
	for _, trace := range []bool{false, true} {
		t.Run(fmt.Sprint("trace=", trace), func(t *testing.T) {
			var mu sync.Mutex
			calls := []string{}
			r := &sourceAdmissionRequester{admit: func(method string, params any) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, method+":"+params.([]any)[0].(string))
			}, finish: func(ctx context.Context, method string, params, out any) error {
				if method == "debug_traceBlockByNumber" {
					return sourceHistoricalTestResult([]any{}, out)
				}
				return sourceHistoricalTestResult(sourceAdmissionBlock(params), out)
			}}
			s, err := NewSourceInMemoryHistoricalSync(r, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			kind := "block"
			if trace {
				kind = "trace"
			}
			required := []store.SourceFilterInterval{{Filter: filter.Object{"type": kind, "interval": float64(1), "offset": float64(0)}, Interval: store.SourceInterval{0, 149}}}
			seen := 0
			for _, err := range s.SyncBlockData(context.Background(), required, nil) {
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				got := append([]string{}, calls...)
				mu.Unlock()
				want := []string{}
				for i := 0; i < 100; i++ {
					want = append(want, "eth_getBlockByNumber:"+Hex(uint64(i)))
					if trace {
						want = append(want, "debug_traceBlockByNumber:"+Hex(uint64(i)))
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("initial admissions differ: got %v want %v", got, want)
				}
				seen++
				break
			}
			if seen != 1 {
				t.Fatalf("yield count %d", seen)
			}
		})
	}
	for _, failFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel joins, error=", failFirst), func(t *testing.T) {
			var admitted, completed atomic.Int32
			expected := errors.New("first block failure")
			r := &sourceAdmissionRequester{admit: func(string, any) { admitted.Add(1) }, finish: func(ctx context.Context, method string, params, out any) error {
				defer completed.Add(1)
				if params.([]any)[0] == "0x0" {
					if failFirst {
						return expected
					}
					return sourceHistoricalTestResult(sourceAdmissionBlock(params), out)
				}
				<-ctx.Done()
				return ctx.Err()
			}}
			s, err := NewSourceInMemoryHistoricalSync(r, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, err := range s.SyncBlockData(context.Background(), []store.SourceFilterInterval{{Filter: filter.Object{"type": "block", "interval": float64(1), "offset": float64(0)}, Interval: store.SourceInterval{0, 149}}}, nil) {
				if failFirst && !errors.Is(err, expected) {
					t.Fatalf("error identity: %v", err)
				}
				if !failFirst && err != nil {
					t.Fatal(err)
				}
				count++
				break
			}
			if count != 1 || admitted.Load() != 100 || completed.Load() != 100 {
				t.Fatalf("count=%d admitted=%d completed=%d", count, admitted.Load(), completed.Load())
			}
		})
	}

	t.Run("synchronous compatibility", func(t *testing.T) {
		calls := 0
		r := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
			calls++
			return sourceHistoricalTestResult(sourceAdmissionBlock(params), out)
		})
		s, err := NewSourceInMemoryHistoricalSync(r, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, err := range s.SyncBlockData(context.Background(), []store.SourceFilterInterval{{Filter: filter.Object{"type": "block", "interval": float64(1), "offset": float64(0)}, Interval: store.SourceInterval{0, 2}}}, nil) {
			if err != nil {
				t.Fatal(err)
			}
			seen++
			break
		}
		if seen != 1 || calls != 3 {
			t.Fatalf("seen=%d calls=%d", seen, calls)
		}
	})
}

// A failed Promise.all member must cancel/join an indefinitely waiting sibling,
// and the sibling's cancellation/error must not replace the initiating error.
func TestSourceHistoricalAcquisitionFirstError(t *testing.T) {
	for _, asynchronous := range []bool{false, true} {
		for _, firstMethod := range []string{"eth_getBlockByNumber", "debug_traceBlockByNumber"} {
			for _, secondError := range []bool{false, true} {
				t.Run(fmt.Sprintf("async=%v/first=%s/opposite-error=%v", asynchronous, firstMethod, secondError), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					first := errors.New("initiating failure")
					opposite := errors.New("later opposite failure")
					var finished atomic.Int32
					finish := sourceHistoricalTestRequester(func(ctx context.Context, method string, params, out any) error {
						defer finished.Add(1)
						if method == firstMethod {
							return first
						}
						<-ctx.Done()
						if secondError {
							return opposite
						}
						return ctx.Err()
					})
					var requester rpc.Requester = finish
					if asynchronous {
						requester = &sourceAdmissionRequester{admit: func(string, any) {}, finish: finish}
					}
					syncer, err := NewSourceInMemoryHistoricalSync(requester, nil, NewSourceHistoricalChildren(nil), SourceHistoricalOptions{})
					if err != nil {
						t.Fatal(err)
					}
					result := make(chan error, 1)
					go func() {
						for _, err := range syncer.SyncBlockData(ctx, []store.SourceFilterInterval{{Filter: filter.Object{"type": "trace"}, Interval: store.SourceInterval{0, 0}}}, nil) {
							result <- err
							return
						}
						result <- nil
					}()
					select {
					case err := <-result:
						if err != first {
							t.Fatalf("lost first error identity: %v", err)
						}
						if finished.Load() != 2 {
							t.Fatalf("returned before both requests joined: %d", finished.Load())
						}
					case <-time.After(3 * time.Second):
						cancel()
						<-result
						t.Fatal("first failure did not cancel sibling without external cancellation")
					}
				})
			}
		}
	}
}
