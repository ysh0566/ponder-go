package runtime

import (
	"context"
	"fmt"
	"strconv"

	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// The enclosing probe replay first executes the real Go coordinator against
// captured source input/RPC responses and the actual PostgreSQL/PGlite store.
// This wrapper observes new production queries; expected rows never seed it.
type sourceHistoryProbeQueryStore struct {
	store    *store.SourceSyncStore
	registry *historicalLiveRegistry
	expected []any
	count    int
	verify   bool
}

func (s *sourceHistoryProbeQueryStore) GetEventData(ctx context.Context, args store.SourceEventDataArgs) (store.SourceEventData, error) {
	if s.verify {
		if s.count >= len(s.expected) {
			return store.SourceEventData{}, fmt.Errorf("refetch made an uncaptured source query")
		}
		actual := filter.Object{"filters": args.Filters, "fromBlock": args.FromBlock, "toBlock": args.ToBlock, "chainId": args.ChainID, "limit": args.Limit}
		if err := s.registry.compare(actual, sourceMultichainWire(s.expected[s.count]), fmt.Sprintf("refetch.query[%d]", s.count)); err != nil {
			return store.SourceEventData{}, err
		}
	}
	s.count++
	return s.store.GetEventData(ctx, args)
}

// sourceHistoryVerifyProbeHelpers is called by the single supplemental probe
// test after successful coordinator replay. These checks add zero source credit.
// Refetch uses real persisted rows produced by that replay, not the captured
// physical_after snapshot. Aggregate values come from actual source invocation
// inputs and are calculated by the production checkpoint helper.
func sourceHistoryVerifyProbeHelpers(ctx context.Context, registry *historicalLiveRegistry, state *sourceMultichainReplayState, entry map[string]any, source *store.SourceSyncStore) error {
	name, _ := entry["name"].(string)
	refetchNames := map[string]bool{"two-chain-refetch": true, "refetch-all-filters-extras": true, "refetch-sparse-slots": true}
	refetch, hasRefetch := entry["refetch"].(map[string]any)
	if hasRefetch != refetchNames[name] {
		return fmt.Errorf("source refetch payload presence/name mismatch: %s", name)
	}
	if hasRefetch {
		if source == nil || state == nil || registry == nil {
			return fmt.Errorf("actual populated native source store/state required")
		}
		if entry["mode"] != "multichain" {
			return fmt.Errorf("unexpected source refetch setup mode")
		}
		input, exists := refetch["input"]
		if !exists {
			return fmt.Errorf("source refetch invocation input required")
		}
		expected, exists := refetch["result"]
		if !exists {
			return fmt.Errorf("source refetch result required")
		}
		value, err := registry.restore(sourceMultichainWire(input))
		if err != nil {
			return err
		}
		events, err := historicalLiveObjects(value)
		if err != nil || len(events) == 0 {
			return fmt.Errorf("nonempty source refetch input: %v", err)
		}
		chains := append([]SourceHistoricalChain{}, sourceHistoryBuild(state.options)...)
		if callbacks, exists := refetch["eventCallbacks"]; exists {
			if name != "refetch-sparse-slots" {
				return fmt.Errorf("unexpected refetch callback override")
			}
			restored, err := registry.restore(sourceMultichainWire(callbacks))
			if err != nil {
				return err
			}
			lists, err := historicalLiveList(restored)
			if err != nil || len(lists) != len(chains) {
				return fmt.Errorf("refetch callback build shape differs")
			}
			for i, list := range lists {
				chains[i].Local.EventCallbacks, err = historicalLiveObjects(list)
				if err != nil {
					return err
				}
			}
		} else if name == "refetch-sparse-slots" {
			return fmt.Errorf("sparse source callback override missing")
		}
		queries, verify := refetch["queries"].([]any)
		if name == "refetch-all-filters-extras" && (!verify || len(queries) == 0) {
			return fmt.Errorf("source all-filter query capture missing")
		}
		observed := &sourceHistoryProbeQueryStore{store: source, registry: registry, expected: queries, verify: verify}
		got, err := RefetchHistoricalSourceEvents(ctx, chains, events, observed, &protocol.Decoder{})
		if err != nil {
			return err
		}
		if observed.count == 0 || verify && observed.count != len(queries) {
			return fmt.Errorf("refetch query count differs: %d/%d", observed.count, len(queries))
		}
		// JSON serializes source sparse array holes as null. Verify the sparse
		// index set separately; do not invent undefined values or drop positions.
		actual := make([]any, len(got))
		present := []any{}
		for i, event := range got {
			if event != nil {
				actual[i] = event
				present = append(present, strconv.Itoa(i))
			}
		}
		if err = registry.compare(actual, sourceMultichainWire(expected), "refetch.result"); err != nil {
			return err
		}
		if indexes, exists := refetch["presentIndices"]; exists {
			if err = registry.compare(present, indexes, "refetch.presentIndices"); err != nil {
				return err
			}
		} else if name == "refetch-sparse-slots" {
			return fmt.Errorf("source sparse own-index capture missing")
		}
	}
	aggregates, hasAggregate := entry["aggregate"].([]any)
	if hasAggregate != (name == "aggregate-checkpoint-tags") {
		return fmt.Errorf("source aggregate payload presence/name mismatch: %s", name)
	}
	if hasAggregate {
		if len(aggregates) != 3 {
			return fmt.Errorf("all three source aggregate states required")
		}
		for i, value := range aggregates {
			item, ok := value.(map[string]any)
			if !ok || item["name"] != []string{"unbounded", "one-complete", "all-complete"}[i] {
				return fmt.Errorf("source aggregate state order differs")
			}
			input, exists := item["input"]
			if !exists {
				return fmt.Errorf("source aggregate invocation input missing")
			}
			restored, err := registry.restore(sourceMultichainWire(input))
			if err != nil {
				return err
			}
			args, err := historicalLiveObject(restored)
			if err != nil {
				return err
			}
			perChain, err := historicalLiveObjects(args["perChainSync"])
			if err != nil || len(perChain) != 2 {
				return fmt.Errorf("source aggregate must retain both chains")
			}
			progress := make([]*SourceSyncProgress, len(perChain))
			for j, chain := range perChain {
				metadata, err := historicalLiveObject(chain["chain"])
				if err != nil {
					return err
				}
				id, err := historicalLiveUint(metadata["id"])
				if err != nil {
					return err
				}
				p, err := historicalLiveObject(chain["syncProgress"])
				if err != nil {
					return err
				}
				start, err := historicalLiveObject(p["start"])
				if err != nil {
					return err
				}
				finalized, err := historicalLiveObject(p["finalized"])
				if err != nil {
					return err
				}
				progress[j] = &SourceSyncProgress{ChainID: id, Start: start, Finalized: finalized, End: sourceLocalOptionalObject(p["end"]), Current: sourceLocalOptionalObject(p["current"])}
			}
			actual := filter.Object{}
			for _, tag := range []SourceSyncProgressTag{SourceSyncStart, SourceSyncEnd, SourceSyncCurrent, SourceSyncFinalized} {
				cp, err := GetSourceOmnichainCheckpoint(progress, tag)
				if err != nil {
					return err
				}
				actual[string(tag)] = filter.Undefined
				if cp != nil {
					actual[string(tag)] = *cp
				}
			}
			outputs, exists := item["outputs"]
			if !exists {
				return fmt.Errorf("source aggregate outputs missing")
			}
			if err = registry.compare(actual, sourceMultichainWire(outputs), fmt.Sprintf("aggregate[%d].outputs", i)); err != nil {
				return err
			}
		}
	}
	return nil
}
