package store

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"reflect"
	"strings"
)

const managedLiveOmnichainLogOnlyVersion = "managed-live-omnichain-logonly-v16"

func logOnlyOptions(o ManagedLiveOptions) bool { return o.runtimeLogFilters != nil }
func logOnlyQuantity(v any) (uint64, bool) {
	if n, ok := v.(*big.Int); ok {
		if n != nil && n.IsUint64() {
			return n.Uint64(), true
		}
		return 0, false
	}
	if n, ok := v.(big.Int); ok {
		return n.Uint64(), n.IsUint64()
	}
	r := reflect.ValueOf(v)
	if !r.IsValid() {
		return 0, false
	}
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if r.Int() >= 0 {
			return uint64(r.Int()), true
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return r.Uint(), true
	case reflect.Float32, reflect.Float64:
		n := r.Float()
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 9007199254740991 && math.Trunc(n) == n {
			return uint64(n), true
		}
	case reflect.String:
		s := r.String()
		if strings.TrimSpace(s) != s || s == "" {
			return 0, false
		}
		base := 10
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			base = 0
		}
		n, ok := new(big.Int).SetString(s, base)
		if ok && n.IsUint64() {
			return n.Uint64(), true
		}
	}
	return 0, false
}
func cloneLogOnlyValue(v reflect.Value) (reflect.Value, error) {
	if !v.IsValid() {
		return v, nil
	}
	if v.CanInterface() {
		if filter.IsUndefined(v.Interface()) {
			return v, nil
		}
		if n, ok := v.Interface().(*big.Int); ok {
			if n == nil {
				return v, nil
			}
			return reflect.ValueOf(new(big.Int).Set(n)), nil
		}
		if n, ok := v.Interface().(big.Int); ok {
			return reflect.ValueOf(*new(big.Int).Set(&n)), nil
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v, nil
		}
		x, e := cloneLogOnlyValue(v.Elem())
		if e != nil {
			return v, e
		}
		o := reflect.New(v.Type()).Elem()
		o.Set(x)
		return o, nil
	case reflect.Map:
		if v.IsNil() {
			return v, nil
		}
		o := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			x, e := cloneLogOnlyValue(it.Value())
			if e != nil {
				return v, e
			}
			o.SetMapIndex(it.Key(), x)
		}
		return o, nil
	case reflect.Slice:
		if v.IsNil() {
			return v, nil
		}
		o := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			x, e := cloneLogOnlyValue(v.Index(i))
			if e != nil {
				return v, e
			}
			o.Index(i).Set(x)
		}
		return o, nil
	case reflect.Array:
		o := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			x, e := cloneLogOnlyValue(v.Index(i))
			if e != nil {
				return v, e
			}
			o.Index(i).Set(x)
		}
		return o, nil
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return v, nil
	default:
		return v, fmt.Errorf("unsupported mutable log-only filter value %s", v.Kind())
	}
}
func normalizeLogOnlyFilters(in map[uint64][]filter.Object, o ManagedLiveOptions) (map[uint64][]filter.Object, error) {
	if !o.runtimeOmnichain || len(in) != len(o.Chains) {
		return nil, ErrManagedLiveBatchConflict
	}
	out := map[uint64][]filter.Object{}
	remaining := uint64(ManagedLiveOmnichainBlockLimit(len(o.Chains)))
	for _, c := range o.Chains {
		span := o.EndBlocks[c.ID] - o.runtimeStarts[c.ID] + 1
		if span > remaining {
			return nil, fmt.Errorf("log-only configured physical range exceeds atomic receipt budget")
		}
		remaining -= span
		rows, ok := in[c.ID]
		if !ok || len(rows) == 0 {
			return nil, ErrManagedLiveBatchConflict
		}
		for _, f := range rows {
			id, ie := logOnlyQuantity(f["chainId"])
			from, fe := logOnlyQuantity(filter.FilterFromBlock(f))
			to, te := logOnlyQuantity(filter.FilterToBlock(f))
			if f["type"] != "log" || !ie || !fe || !te || id != c.ID || from != o.runtimeStarts[id] || to != o.EndBlocks[id] || from == 0 || len(filter.GetFilterFactories(f)) != 0 {
				return nil, ErrManagedLiveBatchConflict
			}
			if _, e := filter.GetFragments(f); e != nil {
				return nil, e
			}
			copy, e := cloneLogOnlyValue(reflect.ValueOf(f))
			if e != nil {
				return nil, e
			}
			frozen := copy.Interface().(map[string]any)
			if _, e = json.Marshal(frozen); e != nil {
				return nil, e
			}
			out[id] = append(out[id], frozen)
		}
	}
	return out, nil
}
func logOnlyPosition(raw string) (checkpoint.Checkpoint, bool) {
	cp, e := checkpoint.Decode(raw)
	if e != nil || cp == checkpoint.Max {
		return cp, false
	}
	if _, ok := liveCanonicalPosition(raw); ok {
		return cp, true
	}
	return cp, cp.EventType == checkpoint.Logs
}
func controlPosition(raw string, c ManagedLiveControl) (checkpoint.Checkpoint, bool) {
	if c.Version == managedLiveOmnichainLogOnlyVersion {
		return logOnlyPosition(raw)
	}
	return liveCanonicalPosition(raw, c.Version == managedLiveOmnichainVersion)
}
func optsPosition(raw string, o ManagedLiveOptions) (checkpoint.Checkpoint, bool) {
	if logOnlyOptions(o) {
		return logOnlyPosition(raw)
	}
	return liveCanonicalPosition(raw, o.runtimeOmnichain)
}
func positionForOptions(raw string, h ManagedLiveHead, o ManagedLiveOptions) bool {
	cp, ok := optsPosition(raw, o)
	_, head, e := normalizeLiveHead(h)
	return ok && e == nil && cp.ChainID == head.ChainID && cp.BlockNumber == head.BlockNumber && cp.BlockTimestamp == head.BlockTimestamp
}
func positionForControl(raw string, h ManagedLiveHead, c ManagedLiveControl) bool {
	cp, ok := controlPosition(raw, c)
	_, head, e := normalizeLiveHead(h)
	return ok && e == nil && cp.ChainID == head.ChainID && cp.BlockNumber == head.BlockNumber && cp.BlockTimestamp == head.BlockTimestamp
}
func positionReachedForOptions(actual, target string, o ManagedLiveOptions) bool {
	a, ok := optsPosition(actual, o)
	b, bok := optsPosition(target, o)
	return ok && bok && a.ChainID == b.ChainID && (a.BlockNumber > b.BlockNumber || a.BlockNumber == b.BlockNumber && (!liveDown(actual) || liveDown(target)))
}
func positionReachedForControl(actual, target string, c ManagedLiveControl) bool {
	a, ok := controlPosition(actual, c)
	b, bok := controlPosition(target, c)
	return ok && bok && a.ChainID == b.ChainID && (a.BlockNumber > b.BlockNumber || a.BlockNumber == b.BlockNumber && (!liveDown(actual) || liveDown(target)))
}
func validateLogOnlyBlock(o ManagedLiveOptions, b ManagedLiveBlock) error {
	if !logOnlyOptions(o) {
		if b.LogEventCount != 0 || b.LogFirstEvent != "" || b.LogLastEvent != "" {
			return ErrManagedLiveBatchConflict
		}
		return nil
	}
	if b.LogEventCount == 0 {
		if b.LogFirstEvent != "" || b.LogLastEvent != "" || b.InputDigest != managedHash([]any{}) {
			return ErrManagedLiveBatchConflict
		}
	} else {
		first, fe := logOnlyPosition(b.LogFirstEvent)
		last, le := logOnlyPosition(b.LogLastEvent)
		_, h, he := normalizeLiveHead(b.Head)
		if !fe || !le || he != nil || first.EventType != checkpoint.Logs || last.EventType != checkpoint.Logs || b.LogFirstEvent > b.LogLastEvent || first.ChainID != h.ChainID || last.ChainID != h.ChainID || first.BlockNumber != h.BlockNumber || last.BlockNumber != h.BlockNumber || first.BlockTimestamp != h.BlockTimestamp || last.BlockTimestamp != h.BlockTimestamp || b.LogEventCount > math.MaxInt64 {
			return ErrManagedLiveBatchConflict
		}
	}
	if b.SourceCheckpoint != "" {
		if !positionForOptions(b.SourceCheckpoint, b.Head, o) || liveDown(b.SourceCheckpoint) {
			return ErrManagedLiveBatchConflict
		}
		if _, canonical := liveCanonicalPosition(b.SourceCheckpoint); !canonical && b.SourceCheckpoint != b.LogLastEvent {
			return ErrManagedLiveBatchConflict
		}
	}
	return nil
}

type RuntimeEventInput struct{ Kind, Checkpoint string }

func (d *ManagedLiveDomains) ValidateRuntimeBlockInputs(b ManagedLiveBlock, events []RuntimeEventInput) error {
	if e := validateLogOnlyBlock(d.live().opts, b); e != nil {
		return e
	}
	if !logOnlyOptions(d.live().opts) {
		return nil
	}
	if uint64(len(events)) != b.LogEventCount {
		return ErrManagedLiveBatchConflict
	}
	previous := ""
	for _, v := range events {
		cp, ok := logOnlyPosition(v.Checkpoint)
		_, h, e := normalizeLiveHead(b.Head)
		if v.Kind != "log" || !ok || e != nil || cp.EventType != checkpoint.Logs || cp.ChainID != h.ChainID || cp.BlockNumber != h.BlockNumber || cp.BlockTimestamp != h.BlockTimestamp || v.Checkpoint < previous {
			return ErrManagedLiveBatchConflict
		}
		previous = v.Checkpoint
	}
	if len(events) > 0 && (events[0].Checkpoint != b.LogFirstEvent || previous != b.LogLastEvent) {
		return ErrManagedLiveBatchConflict
	}
	return nil
}
func validateLogOnlyControl(c ManagedLiveControl) error {
	if c.Version != managedLiveOmnichainLogOnlyVersion {
		if c.LogOnlyLastEvent != "" || len(c.LogOnlyScanHeads) != 0 {
			return ErrManagedLiveBatchConflict
		}
		return nil
	}
	if len(c.LogOnlyScanHeads) > 0 {
		if len(c.LogOnlyScanHeads) != len(c.Cursors) {
			return ErrManagedLiveBatchConflict
		}
		for id, h := range c.LogOnlyScanHeads {
			normalized, cp, err := normalizeLiveHead(h)
			final, fe := checkpoint.Decode(c.FinalizedHeads[id].Checkpoint)
			if err != nil || fe != nil || normalized != h || cp.ChainID != id || cp.BlockNumber != c.HistoryCuts[id] || cp.BlockNumber > final.BlockNumber || cp.BlockTimestamp > final.BlockTimestamp {
				return ErrManagedLiveBatchConflict
			}
		}
	}
	if c.Serving && len(c.LogOnlyScanHeads) != len(c.Cursors) {
		return ErrManagedLiveBatchConflict
	}
	if c.LogOnlyLastEvent != "" {
		cp, ok := logOnlyPosition(c.LogOnlyLastEvent)
		if !ok || cp.EventType != checkpoint.Logs || c.Batches == 0 {
			return ErrManagedLiveBatchConflict
		}
		if _, ok = c.Cursors[cp.ChainID]; !ok {
			return ErrManagedLiveBatchConflict
		}
	}
	return nil
}
func rejectLogOnlyJSON(raw []byte) error {
	var value any
	if e := json.Unmarshal(raw, &value); e != nil {
		return e
	}
	var invalid func(any) bool
	invalid = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for key, child := range x {
				for _, name := range []string{"omnichainEventCount", "omnichainFirstEvent", "omnichainLastEvent", "logOnlyLastEvent", "logOnlyScanHeads", "logOnlyScan"} {
					if strings.EqualFold(key, name) {
						return true
					}
				}
				if invalid(child) {
					return true
				}
			}
		case []any:
			for _, child := range x {
				if invalid(child) {
					return true
				}
			}
		}
		return false
	}
	if invalid(value) {
		return fmt.Errorf("legacy receipt contains log-only v16 fields")
	}
	return nil
}
func validateLegacyLogOnlyExtensions(ops []managedLiveOperation) error {
	for _, op := range ops {
		if e := rejectLogOnlyJSON([]byte(op.Payload)); e != nil {
			return e
		}
	}
	return nil
}
