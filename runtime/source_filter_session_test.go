package runtime

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

func TestSourceLocalPlanningSnapshotIdentity(t *testing.T) {
	session := &filter.Session{}
	f := filter.Object{"type": "block", "chainId": 1, "interval": 1, "offset": 0, "include": []string{"block.number"}}
	filters, cached := sourceLocalPlanning(SourceLocalHistoricalOptions{FilterSession: session, CachedIntervals: []store.SourceIntervalEntry{{Key: f}}}, []filter.Object{f})
	if reflect.ValueOf(filters[0]).Pointer() == reflect.ValueOf(f).Pointer() || reflect.ValueOf(filters[0]).Pointer() != reflect.ValueOf(cached[0].Key).Pointer() {
		t.Fatal("planning must copy maps but preserve Map key identity")
	}
	session.Update(func() { f["include"].([]string)[0] = "block.timestamp"; f["extra"] = 1 })
	if filters[0]["include"].([]string)[0] != "block.number" || filters[0]["extra"] != nil {
		t.Fatal("planning escaped immutable snapshot")
	}
}

type sessionDecodeLogger func(protocol.Object)

func (f sessionDecodeLogger) Debug(o protocol.Object) { f(o) }
func TestSourceHistoryDecodeSessionCanonicalAndUnlocked(t *testing.T) {
	session := &filter.Session{}
	chain := protocol.Object{"id": 1}
	cb := protocol.Object{"type": "contract", "name": "T:E", "chain": chain, "filter": protocol.Object{"type": "log", "include": []string{"block.number"}}, "abiItem": abi.Event{Type: "event", Name: "E", Inputs: []abi.Parameter{{Name: "x", HasName: true, Type: "uint256", Indexed: true}}}}
	item := cb["abiItem"].(abi.Event)
	raw := []protocol.RawEvent{{EventCallbackIndex: 0, Block: protocol.Object{"number": big.NewInt(1)}, Log: protocol.Object{"topics": []string{abi.EventSelector(item)}, "data": "0x"}}, {EventCallbackIndex: 0, Block: protocol.Object{"number": big.NewInt(2)}, Log: protocol.Object{"topics": []string{abi.EventSelector(item), "0x0000000000000000000000000000000000000000000000000000000000000007"}, "data": "0x"}}}
	called := 0
	decoder := &protocol.Decoder{Logger: sessionDecodeLogger(func(protocol.Object) {
		session.Update(func() { cb["filter"].(protocol.Object)["include"] = []string{"block.timestamp"} })
		called++
	})}
	done := make(chan struct{})
	var decoded []protocol.Object
	var err error
	go func() {
		decoded, err = sourceHistoryDecodeSession(chain, []protocol.Object{cb}, session, decoder, raw)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("session lock held across external decoder logger")
	}
	if err != nil || len(decoded) != 1 || called == 0 {
		t.Fatal("actual decode did not execute", decoded, err, called)
	}
	if reflect.ValueOf(decoded[0]["eventCallback"]).Pointer() != reflect.ValueOf(cb).Pointer() || reflect.ValueOf(decoded[0]["chain"]).Pointer() != reflect.ValueOf(chain).Pointer() {
		t.Fatal("snapshot callback leaked to consumer")
	}
}
func TestSourceLocalBuildConcurrentFilterLearning(t *testing.T) {
	s := &filter.Session{}
	f := filter.Object{"type": "block", "chainId": 1, "interval": 1, "offset": 0, "include": []string{"block.number"}}
	cb := protocol.Object{"type": "block", "filter": f}
	o := SourceLocalHistoricalOptions{ChainID: 1, FilterSession: s, EventCallbacks: []protocol.Object{cb}, Children: NewSourceHistoricalChildren(nil)}
	data := store.SourceEventData{Blocks: []filter.Object{{"number": big.NewInt(1), "timestamp": big.NewInt(2)}}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			s.Update(func() { f["include"] = []any{"block.number", i}; f["extra"] = i; delete(f, "extra") })
		}
	}()
	for i := 0; i < 1000; i++ {
		built, err := sourceLocalBuild(o, data)
		if err != nil || len(built) != 1 {
			t.Fatal("actual BuildEvents changed under include learning", built, err)
		}
		decoded, err := sourceHistoryDecodeSession(protocol.Object{"id": 1}, o.EventCallbacks, s, &protocol.Decoder{}, built)
		if err != nil || len(decoded) != 1 || reflect.ValueOf(decoded[0]["eventCallback"]).Pointer() != reflect.ValueOf(cb).Pointer() {
			t.Fatal("actual decode identity changed", err)
		}
	}
	wg.Wait()
}

type localSnapshotStore struct {
	entered chan struct{}
	release chan struct{}
	data    store.SourceEventData
}

func (s *localSnapshotStore) GetEventData(ctx context.Context, _ store.SourceEventDataArgs) (store.SourceEventData, error) {
	close(s.entered)
	select {
	case <-s.release:
		return s.data, nil
	case <-ctx.Done():
		return store.SourceEventData{}, ctx.Err()
	}
}
func (*localSnapshotStore) GetSafeCrashRecoveryBlock(context.Context, uint64, int64) (*store.SourceRecoveryBlock, error) {
	return nil, fmt.Errorf("unexpected recovery read")
}
func (*localSnapshotStore) WithTransaction(context.Context, func(*store.SourceSyncStore) error) error {
	return fmt.Errorf("unexpected acquisition transaction")
}
func TestSourceLocalBuildSnapshotsAfterQuery(t *testing.T) {
	s := &filter.Session{}
	f := filter.Object{"type": "block", "chainId": 1, "interval": 1, "offset": 0, "include": []string{"block.number"}}
	cb := protocol.Object{"type": "block", "filter": f}
	block := filter.Object{"number": "0x1", "timestamp": "0x2"}
	source := &localSnapshotStore{entered: make(chan struct{}), release: make(chan struct{}), data: store.SourceEventData{Blocks: []filter.Object{{"number": big.NewInt(1), "timestamp": big.NewInt(2)}}, Cursor: 1}}
	from, err := checkpoint.Encode(checkpoint.Block(2, 1, 1, false))
	if err != nil {
		t.Fatal(err)
	}
	to, err := checkpoint.Encode(checkpoint.Block(2, 1, 1, true))
	if err != nil {
		t.Fatal(err)
	}
	o := SourceLocalHistoricalOptions{FilterSession: s, ChainID: 1, EventCallbacks: []protocol.Object{cb}, Children: NewSourceHistoricalChildren(nil), Store: source, CachedIntervals: []store.SourceIntervalEntry{{Key: f}}, Progress: &SourceSyncProgress{ChainID: 1, Start: block, End: block, Finalized: block, Current: block}, From: from, To: to, Limit: 10}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	var batch *SourceRawEventBatch
	go func() {
		for value, err := range LocalSourceEvents(ctx, o) {
			if err != nil {
				done <- err
				return
			}
			batch = value
		}
		done <- nil
	}()
	select {
	case <-source.entered:
	case <-ctx.Done():
		t.Fatal("actual local extraction never queried")
	}
	updated := make(chan struct{})
	go func() {
		s.Update(func() { f["include"] = []string{"block.timestamp"}; f["interval"] = 2 })
		close(updated)
	}()
	// The interval mutation is a diagnostic observation key for snapshot timing,
	// not permission for callers to mutate runtime configuration while running.
	select {
	case <-updated:
	case <-ctx.Done():
		close(source.release)
		t.Fatal("session lock held across query await")
	}
	close(source.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Events) != 0 {
		t.Fatal("BuildEvents reused a pre-query filter snapshot", batch)
	}
}
