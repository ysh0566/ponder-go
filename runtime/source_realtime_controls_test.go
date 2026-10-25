package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/bloom"
	"ponder.local/ponder-go/filter"
)

type realtimeControlProvider struct {
	mu               sync.Mutex
	mode             string
	requests         []filter.Object
	started, release chan struct{}
	once             sync.Once
}

func realtimeControlBlock(n uint64) filter.Object {
	parent := n
	if n > 0 {
		parent--
	}
	return filter.Object{"number": Hex(n), "hash": fmt.Sprintf("0x%064x", n), "parentHash": fmt.Sprintf("0x%064x", parent), "timestamp": Hex(n), "logsBloom": bloom.ZeroLogsBloom, "transactions": []filter.Object{}}
}
func (p *realtimeControlProvider) Call(ctx context.Context, method string, params, out any) error {
	p.mu.Lock()
	p.requests = append(p.requests, filter.Object{"method": method, "params": params})
	p.mu.Unlock()
	if p.mode == "wait" {
		p.once.Do(func() { close(p.started) })
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if p.mode == "reject" {
		return errors.New("provider failure")
	}
	switch method {
	case "eth_getLogs":
		*out.(*[]filter.Object) = []filter.Object{}
	case "eth_getBlockByNumber":
		n, err := sourceHistoricalNumber(params.([]any)[0])
		if err != nil {
			return err
		}
		*out.(*filter.Object) = realtimeControlBlock(n)
	default:
		return fmt.Errorf("unexpected native control request %s", method)
	}
	return nil
}
func realtimeControlError(err error) filter.Object {
	name := "Error"
	if typed, ok := err.(interface{ SourceErrorName() string }); ok {
		name = typed.SourceErrorName()
	}
	return filter.Object{"name": name, "message": err.Error()}
}
func TestSourceRealtimeControls(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/source-realtime-controls.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceCredit int `json:"source_credit"`
		Cases        []struct {
			Name  string
			Input struct {
				Kind            string
				Chain           SourceRealtimeChain
				Finalized       filter.Object
				Blocks, Filters []filter.Object
				Attempts        int
				Killed          bool
				ProviderMode    string `json:"provider_mode"`
			}
			Result any
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 12 || fixture.SourceCredit != 0 {
		t.Fatal("complete actual-source controls required")
	}
	for _, vector := range fixture.Cases {
		t.Run(vector.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			input := vector.Input
			provider := &realtimeControlProvider{mode: input.ProviderMode, requests: []filter.Object{}, started: make(chan struct{}), release: make(chan struct{})}
			logger := &realtimeLiveLogger{warnings: []any{}}
			syncer, err := NewSourceRealtimeSync(provider, nil, NewSourceHistoricalChildren(nil), input.Finalized, input.Filters, input.Chain, SourceRealtimeOptions{Logger: logger, IsKilled: func() bool { return input.Killed }})
			if err != nil {
				t.Fatal(err)
			}
			events := []filter.Object{}
			checks := filter.Object{}
			callbacks := []bool{}
			var callbackMu sync.Mutex
			callback := func(value bool) { callbackMu.Lock(); defer callbackMu.Unlock(); callbacks = append(callbacks, value) }
			callbackCopy := func() []bool { callbackMu.Lock(); defer callbackMu.Unlock(); return append([]bool{}, callbacks...) }
			summary := func(event SourceRealtimeEvent) filter.Object {
				n, e := sourceHistoricalNumber(event.Block["number"])
				if e != nil {
					t.Fatal(e)
				}
				result := filter.Object{"type": event.Type, "number": n}
				if event.Type == "block" {
					result["has_callback"] = event.BlockCallback != nil && reflect.ValueOf(event.BlockCallback).Pointer() == reflect.ValueOf(callback).Pointer()
					result["matched"] = event.HasMatchedFilter
				}
				return result
			}
			drain := func(sequence iter.Seq2[SourceRealtimeEvent, error], accept bool) {
				for event, err := range sequence {
					if err != nil {
						t.Fatal(err)
					}
					events = append(events, summary(event))
					if accept && event.Type == "block" && event.BlockCallback != nil {
						event.BlockCallback(true)
					}
				}
			}
			numbers := func() []uint64 {
				values := []uint64{}
				for _, b := range syncer.UnfinalizedBlocks() {
					n, e := sourceHistoricalNumber(b["number"])
					if e != nil {
						t.Fatal(e)
					}
					values = append(values, n)
				}
				return values
			}
			switch input.Kind {
			case "callback":
				drain(syncer.Sync(ctx, input.Blocks[0], callback), true)
				checks["before_duplicate"] = callbackCopy()
				drain(syncer.Sync(ctx, input.Blocks[0], callback), false)
			case "early_return":
				next, stop := iter.Pull2(syncer.Sync(ctx, input.Blocks[0], callback))
				event, err, ok := next()
				if err != nil || !ok {
					t.Fatal("first yield", err)
				}
				events = append(events, summary(event))
				stop()
				checks["retained_before_next"] = numbers()
				drain(syncer.Sync(ctx, input.Blocks[0], callback), false)
				drain(syncer.Sync(ctx, input.Blocks[1], callback), true)
			case "missing":
				drain(syncer.Sync(ctx, input.Blocks[0], callback), false)
			case "retry":
				at, same, errs := []int{}, []bool{}, []filter.Object{}
				original := errors.New("retry error")
				for i := 1; i <= input.Attempts; i++ {
					if err := syncer.OnError(original, nil); err != nil {
						at = append(at, i)
						same = append(same, err == original)
						errs = append(errs, realtimeControlError(err))
					}
				}
				checks["thrown_at"], checks["same_error"], checks["errors"] = at, same, errs
			case "reset":
				original := errors.New("retry error")
				for range 9 {
					if err := syncer.OnError(original, nil); err != nil {
						t.Fatal(err)
					}
				}
				drain(syncer.Sync(ctx, input.Blocks[0], callback), false)
				at := []int{}
				for i := 1; i <= 2; i++ {
					if err := syncer.OnError(original, nil); err != nil {
						at = append(at, i)
					}
				}
				checks["thrown_at"] = at
			case "concurrent_duplicate":
				type yielded struct {
					event SourceRealtimeEvent
					err   error
					ok    bool
				}
				next, stop := iter.Pull2(syncer.Sync(ctx, input.Blocks[0], callback))
				defer stop()
				result := make(chan yielded, 1)
				go func() { e, err, ok := next(); result <- yielded{e, err, ok} }()
				select {
				case <-provider.started:
				case <-ctx.Done():
					t.Fatal("source-equivalent fetch did not start")
				}
				drain(syncer.Sync(ctx, input.Blocks[0], callback), false)
				checks["before_release"] = callbackCopy()
				close(provider.release)
				select {
				case value := <-result:
					if value.err != nil || !value.ok {
						t.Fatal("original pending yield", value.err)
					}
					events = append(events, summary(value.event))
					value.event.BlockCallback(true)
				case <-ctx.Done():
					t.Fatal("pending fetch blocked")
				}
				if _, err, ok := next(); err != nil || ok {
					t.Fatal("unexpected extra event", err)
				}
			case "serialized":
				first, stopFirst := iter.Pull2(syncer.Sync(ctx, input.Blocks[0], callback))
				defer stopFirst()
				event, err, ok := first()
				if err != nil || !ok {
					t.Fatal(err)
				}
				events = append(events, summary(event))
				second, stopSecond := iter.Pull2(syncer.Sync(ctx, input.Blocks[1], callback))
				defer stopSecond()
				type yielded struct {
					event SourceRealtimeEvent
					err   error
					ok    bool
				}
				result := make(chan yielded, 1)
				entered := make(chan struct{})
				go func() { close(entered); e, err, ok := second(); result <- yielded{e, err, ok} }()
				<-entered
				select {
				case <-result:
					t.Fatal("second reconciliation escaped held source-equivalent lock")
				case <-time.After(10 * time.Millisecond):
					checks["second_waited"] = true
				}
				stopFirst()
				select {
				case value := <-result:
					if value.err != nil || !value.ok {
						t.Fatal("second pending yield", value.err)
					}
					events = append(events, summary(value.event))
					value.event.BlockCallback(true)
				case <-ctx.Done():
					t.Fatal("early return did not release reconciliation lock")
				}
				for {
					event, err, ok := second()
					if err != nil {
						t.Fatal(err)
					}
					if !ok {
						break
					}
					events = append(events, summary(event))
				}
			case "failure_duplicate":
				drain(syncer.Sync(ctx, input.Blocks[0], callback), false)
				drain(syncer.Sync(ctx, input.Blocks[0], callback), false)
			default:
				t.Fatal("unknown actual-source control")
			}
			warnings := []filter.Object{}
			logger.mu.Lock()
			for _, value := range logger.warnings {
				item := filter.Object{}
				for key, value := range value.(filter.Object) {
					if key == "error" {
						value = realtimeControlError(value.(error))
					}
					item[key] = value
				}
				warnings = append(warnings, item)
			}
			logger.mu.Unlock()
			provider.mu.Lock()
			requests := append([]filter.Object{}, provider.requests...)
			provider.mu.Unlock()
			sort.Slice(requests, func(i, j int) bool {
				a, _ := json.Marshal(requests[i])
				b, _ := json.Marshal(requests[j])
				return string(a) < string(b)
			})
			actual := filter.Object{"events": events, "checks": checks, "callbacks": callbackCopy(), "warnings": warnings, "requests": requests, "unfinalized": numbers()}
			encoded, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var normalized any
			if err = json.Unmarshal(encoded, &normalized); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalized, vector.Result) {
				expected, _ := json.Marshal(vector.Result)
				t.Fatalf("actual pinned realtime control differs\ngot %s\nwant %s", encoded, expected)
			}
		})
	}
}
