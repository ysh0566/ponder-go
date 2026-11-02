package runtime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"strings"
)

var ErrManagedRealtimeFinality = errors.New("managed realtime branch crosses finalized identity")

// Restore only authenticated, immutable header identities. No requester,
// iterator, callback, context or pending acquisition from an old epoch survives.
func NewManagedSourceRealtimeSync(requester rpc.Requester, children *SourceHistoricalChildren, finalized filter.Object, retained []filter.Object, filters []filter.Object, chain SourceRealtimeChain) (*SourceRealtimeSync, error) {
	s, err := NewSourceRealtimeSync(requester, nil, children, sourceRealtimeLight(finalized), filters, chain, SourceRealtimeOptions{})
	if err != nil {
		return nil, err
	}
	previous := sourceRealtimeLight(finalized)
	n, err := sourceHistoricalNumber(previous["number"])
	if err != nil {
		return nil, err
	}
	timestamp, err := sourceHistoricalNumber(previous["timestamp"])
	if err != nil {
		return nil, err
	}
	for _, head := range retained {
		next, err := sourceHistoricalNumber(head["number"])
		if err != nil {
			return nil, err
		}
		at, err := sourceHistoricalNumber(head["timestamp"])
		if err != nil || next != n+1 || at < timestamp || head["parentHash"] != previous["hash"] {
			return nil, fmt.Errorf("managed realtime retained headers are not one authenticated branch")
		}
		copy := sourceRealtimeLight(head)
		s.unfinalized = append(s.unfinalized, copy)
		previous, n, timestamp = copy, next, at
	}
	return s, nil
}

// Reuse the real source fetch/filter/reconcile pipeline with explicit errors.
// The owner discards this source epoch on an error; legacy retry semantics stay
// unchanged in Sync. Same-height hash changes are therefore real reorg events.
func (s *SourceRealtimeSync) SyncManaged(ctx context.Context, block filter.Object) iter.Seq2[SourceRealtimeEvent, error] {
	return func(yield func(SourceRealtimeEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(SourceRealtimeEvent{}, err)
			return
		}
		s.stateMu.RLock()
		duplicate := s.latestLocked()["hash"] == block["hash"]
		s.stateMu.RUnlock()
		if duplicate {
			return
		}
		data, err := s.fetchBlockEventData(ctx, block)
		if err == nil {
			s.reconcileMu.Lock()
			_, err = s.reconcileBlock(ctx, data, nil, yield)
			s.reconcileMu.Unlock()
		}
		if err != nil {
			if strings.Contains(err.Error(), "unrecoverable") {
				err = errors.Join(ErrManagedRealtimeFinality, err)
			}
			yield(SourceRealtimeEvent{}, err)
		}
	}
}

// ManagedSourceEventGenerator preserves the original one-next-before-yield
// async adapter and supplies a cancel-and-join owner to the runtime driver.
func ManagedSourceEventGenerator(ctx context.Context, seq iter.Seq2[*SourceEventBatch, error]) (SourceEventGenerator, func()) {
	return sourceHistoryAsync(ctx, seq)
}
