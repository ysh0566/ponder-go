package historical

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/store"
)

// ManagedOptions opts into a NEW managed finite namespace. BuildID is the
// application's handler-code version contract; Go function pointers are not a
// stable code identity. The actual filter/range/ABI plan is independently hashed.
// This API performs no live cutover, realtime polling, reorg, or HTTP serving.
type ManagedOptions struct {
	Historical                          Options
	BuildID                             string
	ViewsSchema                         string
	HeartbeatInterval, HeartbeatTimeout time.Duration
}

type ManagedResult struct {
	Historical Result
	Ready      bool
}

// ManagedRunner does not expose its raw owner, preventing normal callers from
// interleaving business work between joined history and activation. Escaped
// callback databases are also sealed at the owned gate before activation.
type ManagedRunner struct {
	runner  *Runner
	started atomic.Bool
}

func OpenManagedPostgres(ctx context.Context, o ManagedOptions) (*ManagedRunner, error) {
	return openManaged(ctx, o, indexing.OpenManagedOwnedPostgres)
}
func OpenManagedPGlite(ctx context.Context, o ManagedOptions) (*ManagedRunner, error) {
	return openManaged(ctx, o, indexing.OpenManagedOwnedPGlite)
}
func openManaged(ctx context.Context, o ManagedOptions, opener func(context.Context, indexing.OwnedOptions, store.ManagedFiniteOptions) (*indexing.Owned, error)) (*ManagedRunner, error) {
	if o.BuildID == "" {
		return nil, fmt.Errorf("managed finite BuildID required")
	}
	r, err := open(ctx, o.Historical, func(ctx context.Context, owned indexing.OwnedOptions) (*indexing.Owned, error) {
		// open has acquired the exclusive executable session and validated every
		// finite source before this callback. No managed DDL happens before here.
		m, err := managedPlan(o.Historical.Build.Executable())
		if err != nil {
			return nil, err
		}
		m.BuildID, m.ViewsSchema = o.BuildID, o.ViewsSchema
		m.HeartbeatInterval, m.HeartbeatTimeout = o.HeartbeatInterval, o.HeartbeatTimeout
		return opener(ctx, owned, m)
	})
	if err != nil {
		return nil, err
	}
	return &ManagedRunner{runner: r}, nil
}

// Run is once per owner, including failed/unknown attempts. Close and reopen to
// recover durable state; never blindly retry activation with a stale owner.
func (r *ManagedRunner) Run(ctx context.Context) (result ManagedResult, err error) {
	if !r.started.CompareAndSwap(false, true) {
		return result, fmt.Errorf("managed finite runner already attempted; close and reopen")
	}
	result.Historical, err = r.runner.Run(ctx)
	if err != nil {
		return result, err
	}
	if !result.Historical.Completed || len(result.Historical.finiteEnds) == 0 {
		return result, fmt.Errorf("managed activation requires completed, joined history")
	}
	if err = r.runner.owner.ActivateManagedFinite(ctx, result.Historical.finiteEnds); err != nil {
		return result, err
	}
	result.Ready, err = r.runner.owner.ManagedReady(ctx)
	if err == nil && !result.Ready {
		err = fmt.Errorf("managed activation did not establish durable ready")
	}
	return result, err
}
func (r *ManagedRunner) Close(ctx context.Context) error { return r.runner.Close(ctx) }
func (r *ManagedRunner) Ready(ctx context.Context) (bool, error) {
	return r.runner.owner.ManagedReady(ctx)
}
func (r *ManagedRunner) Metadata(ctx context.Context) (store.AppMetadata, error) {
	return r.runner.owner.ManagedMetadata(ctx)
}

func managedPlan(b *config.ExecutableSourceIndexingBuild) (store.ManagedFiniteOptions, error) {
	m := store.ManagedFiniteOptions{EndBlocks: map[uint64]uint64{}}
	if b == nil || len(b.Chains) == 0 || len(b.EventCallbacks) != len(b.Chains) || len(b.SetupCallbacks) != len(b.Chains) {
		return m, fmt.Errorf("complete managed plan required")
	}
	chains := make([]any, len(b.Chains))
	for i, c := range b.Chains {
		id, err := uintQuantity(c["id"])
		if err != nil {
			return m, err
		}
		name, ok := c["name"].(string)
		if !ok || name == "" {
			return m, fmt.Errorf("managed chain name required")
		}
		m.Chains = append(m.Chains, store.MigrationChain{ID: id, Name: name})
		stableChain := map[string]any{}
		for key, value := range c {
			// Transport endpoints may change without changing the indexing plan.
			// All other (including unknown future) chain fields remain bound.
			if key != "rpc" && key != "ws" {
				stableChain[key] = value
			}
		}
		chains[i] = stableChain
		found := false
		for _, cb := range b.EventCallbacks[i] {
			f, ok := cb["filter"].(map[string]any)
			if !ok {
				return m, fmt.Errorf("managed callback filter required")
			}
			end, err := uintQuantity(f["toBlock"])
			if err != nil {
				return m, err
			}
			if !found || end > m.EndBlocks[id] {
				m.EndBlocks[id] = end
			}
			found = true
		}
		if !found {
			return m, fmt.Errorf("managed finite chain requires endBlock")
		}
	}
	callbacks := func(groups [][]filter.Object) ([]any, error) {
		out := make([]any, len(groups))
		for i, group := range groups {
			items := make([]any, len(group))
			for j, cb := range group {
				chain, ok := cb["chain"].(map[string]any)
				id, chainErr := uintQuantity(chain["id"])
				if !ok || chainErr != nil || id != m.Chains[i].ID || chain["name"] != m.Chains[i].Name {
					return nil, fmt.Errorf("managed callback chain identity mismatch")
				}
				copy := map[string]any{}
				for k, v := range cb {
					switch k {
					case "fn":
						continue // ONLY the known executable callback field.
					case "chain":
						copy[k] = chains[i]
					case "filter":
						f, ok := v.(map[string]any)
						if !ok {
							return nil, fmt.Errorf("invalid managed filter")
						}
						clean := map[string]any{}
						for fk, fv := range f {
							if fk != "include" {
								clean[fk] = fv
							}
						}
						// include is learned projection/cache selection, not the event
						// filter. Exclude ONLY this exact callback.filter.include.
						copy[k] = clean
					default:
						copy[k] = v
					}
				}
				items[j] = copy
			}
			out[i] = items
		}
		return out, nil
	}
	events, err := callbacks(b.EventCallbacks)
	if err != nil {
		return m, err
	}
	setup, err := callbacks(b.SetupCallbacks)
	if err != nil {
		return m, err
	}
	functions := make([]any, len(b.IndexingFunctions))
	for i, f := range b.IndexingFunctions {
		copy := map[string]any{}
		for k, v := range f {
			if k != "fn" {
				copy[k] = v
			}
		}
		functions[i] = copy
	}
	plan := map[string]any{"version": "managed-finite-plan-v1", "ordering": b.Ordering, "chains": chains, "contracts": b.Contracts, "events": events, "setup": setup, "functions": functions}
	normal, err := managedStableValue(reflect.ValueOf(plan), map[uintptr]bool{})
	if err != nil {
		return m, err
	}
	encoded, err := json.Marshal(normal)
	if err != nil {
		return m, err
	}
	sum := sha256.Sum256(encoded)
	m.PlanID = hex.EncodeToString(sum[:])
	return m, nil
}

// Every value is type-tagged, so undefined cannot collide with user data whose
// keys resemble a marker. Shared objects are allowed; actual cycles fail closed.
func managedStableValue(v reflect.Value, active map[uintptr]bool) (any, error) {
	if !v.IsValid() {
		return []any{"null"}, nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return []any{"null"}, nil
		}
		return managedStableValue(v.Elem(), active)
	}
	if v.CanInterface() && filter.IsUndefined(v.Interface()) {
		return []any{"undefined"}, nil
	}
	switch v.Kind() {
	case reflect.Bool:
		return []any{"bool", v.Bool()}, nil
	case reflect.String:
		return []any{"string", v.String()}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return []any{"integer", strconv.FormatInt(v.Int(), 10)}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return []any{"integer", strconv.FormatUint(v.Uint(), 10)}, nil
	case reflect.Float32, reflect.Float64:
		n := v.Float()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("nonfinite managed plan number")
		}
		return []any{"number", strconv.FormatFloat(n, 'g', -1, 64)}, nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("managed plan map keys must be strings")
		}
		if v.IsNil() {
			return []any{"null-map"}, nil
		}
		p := v.Pointer()
		if active[p] {
			return nil, fmt.Errorf("cyclic managed plan")
		}
		active[p] = true
		defer delete(active, p)
		out := map[string]any{}
		it := v.MapRange()
		for it.Next() {
			x, err := managedStableValue(it.Value(), active)
			if err != nil {
				return nil, err
			}
			out[it.Key().String()] = x
		}
		return []any{"object", out}, nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				return []any{"null-array"}, nil
			}
			if v.Len() > 0 {
				p := v.Pointer()
				if active[p] {
					return nil, fmt.Errorf("cyclic managed plan")
				}
				active[p] = true
				defer delete(active, p)
			}
		}
		out := make([]any, v.Len())
		for i := range out {
			x, err := managedStableValue(v.Index(i), active)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return []any{"array", out}, nil
	default:
		return nil, fmt.Errorf("unsupported managed plan value %s", v.Type())
	}
}
