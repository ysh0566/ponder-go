package store

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strings"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
)

const managedLivePagedLogOnlyVersion = "managed-live-omnichain-logonly-v17"

const managedLiveMixedVersion = "managed-live-omnichain-mixed-v1"

const managedLiveAccountScanVersion = "managed-live-omnichain-account-scan-v1"

func logOnlyVersion(version string) bool {
	return version == managedLivePagedLogOnlyVersion || version == managedLiveMixedVersion || version == managedLiveAccountScanVersion
}

func logOnlyOptions(o ManagedLiveOptions) bool { return o.runtimeLogFilters != nil }

// RuntimeBlockPeriod returns the positive sampling period used when checking
// finalized Omnichain block receipts. Source filters retain their signed interval.
func RuntimeBlockPeriod(interval any) (uint64, bool) {
	period, ok := logOnlyInteger(interval, true)
	return period, ok && period > 0 && period <= 1<<53-1
}

func logOnlyQuantity(v any) (uint64, bool) {
	return logOnlyInteger(v, false)
}

// Block periods use magnitude. All other scan quantities must be nonnegative.
func logOnlyInteger(v any, magnitude bool) (uint64, bool) {
	if n, ok := v.(big.Int); ok {
		v = &n
	}
	if n, ok := v.(*big.Int); ok {
		if n == nil {
			return 0, false
		}
		if magnitude && n.Sign() < 0 {
			n = new(big.Int).Abs(n)
		}
		if !n.IsUint64() {
			return 0, false
		}
		return n.Uint64(), true
	}
	r := reflect.ValueOf(v)
	if !r.IsValid() {
		return 0, false
	}
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := r.Int()
		if n >= 0 {
			return uint64(n), true
		}
		if magnitude {
			return -uint64(n), true
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return r.Uint(), true
	case reflect.Float32, reflect.Float64:
		n := r.Float()
		if magnitude {
			n = math.Abs(n)
		}
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
		if ok {
			return logOnlyInteger(n, magnitude)
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
	for _, c := range o.Chains {
		rows, ok := in[c.ID]
		if ok && rows == nil {
			continue // This chain supplies a closing block callback.
		}
		if !ok || len(rows) == 0 {
			return nil, ErrManagedLiveBatchConflict
		}
		first, last := uint64(math.MaxUint64), uint64(0)
		factories := map[string][]string{}
		blockSources := map[string]bool{}
		for _, f := range rows {
			id, ie := logOnlyQuantity(f["chainId"])
			from, fe := logOnlyQuantity(filter.FilterFromBlock(f))
			to, te := logOnlyQuantity(filter.FilterToBlock(f))
			kind := f["type"]
			if kind == "block" {
				period, validPeriod := RuntimeBlockPeriod(f["interval"])
				offset, oe := logOnlyQuantity(f["offset"])
				name, _ := f["sourceId"].(string)
				if !validPeriod || !oe || offset >= period || name == "" || blockSources[name] {
					return nil, ErrManagedLiveBatchConflict
				}
				blockSources[name] = true
			} else if kind != "log" && kind != "transaction" && kind != "transfer" && !(kind == "trace" && f["callType"] == "CALL") {
				return nil, ErrManagedLiveBatchConflict
			}
			if !ie || !fe || !te || id != c.ID || from > to || (!o.runtimeFactories && len(filter.GetFilterFactories(f)) != 0) {
				return nil, ErrManagedLiveBatchConflict
			}
			first, last = min(first, from), max(last, to)
			for _, factory := range filter.GetFilterFactories(f) {
				fid, ok := logOnlyQuantity(factory["chainId"])
				key, hasID := factory["id"].(string)
				parts := filter.FactoryFragmentIDs(factory)
				if prior, exists := factories[key]; exists && !slices.Equal(prior, parts) {
					return nil, ErrManagedLiveBatchConflict
				}
				factories[key] = parts
				if !ok || fid != id || !hasID || key == "" {
					return nil, ErrManagedLiveBatchConflict
				}
			}
			if _, e := logFragmentRanges(f, from, to); e != nil {
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
		if first != o.runtimeStarts[c.ID] || last != o.EndBlocks[c.ID] {
			return nil, ErrManagedLiveBatchConflict
		}
	}
	if len(out) == 0 {
		return nil, ErrManagedLiveBatchConflict
	}
	return out, nil
}

func scheduledBlockSources(filters []filter.Object, height uint64) []string {
	var names []string
	for _, f := range filters {
		if f["type"] == "block" && filter.IsBlockFilterMatched(f, filter.Object{"number": height}) {
			names = append(names, f["sourceId"].(string))
		}
	}
	return names
}

// A sparse chain can skip heights. Its cursor can be a block
// boundary or an event from one of its configured source filters.
func sparseEventAllowed(cp checkpoint.Checkpoint, filters []filter.Object, kind string) bool {
	for _, f := range filters {
		filterKind, _ := f["type"].(string)
		if (kind == "" || kind == filterKind) && cp.MatchesEvent(filterKind) && filter.IsBlockInFilter(f, float64(cp.BlockNumber)) {
			if filterKind == "block" && !filter.IsBlockFilterMatched(f, filter.Object{"number": cp.BlockNumber}) {
				continue
			}
			return true
		}
	}
	return false
}

func controlPosition(raw string, c ManagedLiveControl) (checkpoint.Checkpoint, bool) {
	allowBlock := c.Version == managedLiveMixedVersion || c.Version == managedLiveOmnichainVersion || c.Version == managedLiveAccountScanVersion
	cp, ok := liveCanonicalPosition(raw, allowBlock)
	if ok || !logOnlyVersion(c.Version) {
		return cp, ok
	}
	cp, err := checkpoint.Decode(raw)
	return cp, err == nil && (cp.MatchesEvent("log") || c.Version == managedLiveAccountScanVersion && (cp.MatchesEvent("transaction") || cp.MatchesEvent("transfer")))
}

func optsPosition(raw string, o ManagedLiveOptions) (checkpoint.Checkpoint, bool) {
	cp, err := checkpoint.Decode(raw)
	if err != nil {
		return cp, false
	}
	if filters := o.runtimeLogFilters[cp.ChainID]; filters != nil {
		if sparseEventAllowed(cp, filters, "") {
			return cp, true
		}
		return liveCanonicalPosition(raw)
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
	head, err := checkpoint.Decode(b.Head.Checkpoint)
	if err != nil {
		return ErrManagedLiveBatchConflict
	}
	filters := o.runtimeLogFilters[head.ChainID]
	if filters == nil {
		if b.LogEventCount != 0 || b.LogFirstEvent != "" || b.LogLastEvent != "" {
			return ErrManagedLiveBatchConflict
		}
		return nil
	}
	due := uint64(len(scheduledBlockSources(filters, head.BlockNumber)))
	if b.LogEventCount < due {
		return ErrManagedLiveBatchConflict
	}
	if b.LogEventCount == 0 {
		if b.LogFirstEvent != "" || b.LogLastEvent != "" || b.InputDigest != managedHash([]any{}) {
			return ErrManagedLiveBatchConflict
		}
	} else {
		first, fe := checkpoint.Decode(b.LogFirstEvent)
		last, le := checkpoint.Decode(b.LogLastEvent)
		_, h, he := normalizeLiveHead(b.Head)
		if due > 0 && (!last.MatchesEvent("block") || first.MatchesEvent("block") != (b.LogEventCount == due)) {
			return ErrManagedLiveBatchConflict
		}
		if fe != nil || le != nil || he != nil || !sparseEventAllowed(first, filters, "") || !sparseEventAllowed(last, filters, "") || b.LogFirstEvent > b.LogLastEvent || first.ChainID != h.ChainID || last.ChainID != h.ChainID || first.BlockNumber != h.BlockNumber || last.BlockNumber != h.BlockNumber || first.BlockTimestamp != h.BlockTimestamp || last.BlockTimestamp != h.BlockTimestamp || b.LogEventCount > math.MaxInt64 {
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

type RuntimeEventInput struct{ Kind, Checkpoint, BlockSourceID string }

func (d *ManagedLiveDomains) ValidateRuntimeBlockInputs(b ManagedLiveBlock, events []RuntimeEventInput) error {
	if e := validateLogOnlyBlock(d.live().opts, b); e != nil {
		return e
	}
	head, err := checkpoint.Decode(b.Head.Checkpoint)
	if err != nil {
		return ErrManagedLiveBatchConflict
	}
	if d.live().opts.runtimeLogFilters[head.ChainID] == nil {
		return nil
	}
	if uint64(len(events)) != b.LogEventCount {
		return ErrManagedLiveBatchConflict
	}
	// All block callbacks at this height share a checkpoint. Verify their
	// source IDs in registration order before dispatching any of the handlers.
	due := scheduledBlockSources(d.live().opts.runtimeLogFilters[head.ChainID], head.BlockNumber)
	start := len(events) - len(due) // The receipt already checked the event count.
	for i, source := range due {
		v := events[start+i]
		if v.Kind != "block" || v.BlockSourceID != source {
			return ErrManagedLiveBatchConflict
		}
	}
	if start > 0 && events[start-1].Kind == "block" {
		return ErrManagedLiveBatchConflict
	}
	previous := ""
	for _, v := range events {
		cp, decodeErr := checkpoint.Decode(v.Checkpoint)
		_, h, e := normalizeLiveHead(b.Head)
		if decodeErr != nil || e != nil || !cp.MatchesEvent(v.Kind) || !sparseEventAllowed(cp, d.live().opts.runtimeLogFilters[head.ChainID], v.Kind) || cp.ChainID != h.ChainID || cp.BlockNumber != h.BlockNumber || cp.BlockTimestamp != h.BlockTimestamp || v.Checkpoint < previous {
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
	if !logOnlyVersion(c.Version) {
		if c.LogOnlyLastEvent != "" || len(c.LogOnlyScanHeads) != 0 {
			return ErrManagedLiveBatchConflict
		}
		return nil
	}
	if len(c.LogOnlyScanHeads) > 0 {
		if c.Version == managedLivePagedLogOnlyVersion && len(c.LogOnlyScanHeads) != len(c.Cursors) || c.Version == managedLiveMixedVersion && len(c.LogOnlyScanHeads) >= len(c.Cursors) {
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
	if c.Serving && len(c.LogOnlyScanHeads) == 0 {
		return ErrManagedLiveBatchConflict
	}
	if c.LogOnlyLastEvent != "" {
		cp, ok := controlPosition(c.LogOnlyLastEvent, c)
		if !ok || !(cp.MatchesEvent("log") || cp.MatchesEvent("transaction") || cp.MatchesEvent("transfer") || c.Version == managedLiveAccountScanVersion && cp.MatchesEvent("block")) || c.Batches == 0 {
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
		return fmt.Errorf("non-log receipt contains log-only fields")
	}
	return nil
}
func rejectLogOnlyOperationFields(ops []managedLiveOperation) error {
	for _, op := range ops {
		if e := rejectLogOnlyJSON([]byte(op.Payload)); e != nil {
			return e
		}
	}
	return nil
}
