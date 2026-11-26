package store

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestLogFactoryScanEvidence(t *testing.T) {
	parent := "0x" + strings.Repeat("11", 20)
	child := "0x" + strings.Repeat("22", 20)
	created := "0x" + strings.Repeat("ab", 32)
	ping := abi.Event{Type: "event", Name: "Ping", Inputs: []abi.Parameter{{Name: "n", Type: "uint256"}}}
	factory := filter.Object{"id": "children", "type": "log", "chainId": 1, "address": parent, "eventSelector": created, "childAddressLocation": "topic1", "fromBlock": 1, "toBlock": 2}
	f := filter.Object{"type": "log", "chainId": 1, "fromBlock": 2, "toBlock": 8, "topic0": abi.EventSelector(ping), "address": factory}
	group := []filter.Object{f}
	callbacks, err := logOnlyABICallbacks(map[uint64][]filter.Object{1: group}, map[uint64][]any{1: {ping}})
	if err != nil {
		t.Fatal(err)
	}
	opts := ManagedLiveOptions{runtimeLogFilters: map[uint64][]filter.Object{1: group}, runtimeLogCallbacks: callbacks}
	for _, tc := range []struct {
		name string
		want error
	}{{"empty-gap", nil}, {"unprocessed-child", ErrManagedLivePendingActions}, {"wrong-parent-hash", ErrManagedLiveBatchConflict}, {"parent-unavailable", errors.New("parent unavailable")}} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			node := logScanTestRPC(func(q map[string]any) ([]filter.Object, error) {
				requests = append(requests, fmt.Sprint(q["fromBlock"], "-", q["toBlock"]))
				topics := q["topics"].([]any)
				log := filter.Object{"logIndex": "0x0", "transactionIndex": "0x0", "data": "0x"}
				if topics[0] == created {
					if q["address"] != parent {
						t.Fatal(q)
					}
					if tc.name == "parent-unavailable" {
						return nil, tc.want
					}
					log["blockNumber"], log["blockHash"], log["address"], log["topics"] = "0x1", sparseRangeHead(1, 1).Hash, parent, []any{created, "0x" + strings.Repeat("0", 24) + child[2:]}
					if tc.name == "wrong-parent-hash" {
						log["blockHash"] = sparseRangeHead(1, 2).Hash
					}
					return []filter.Object{log}, nil
				}
				if fmt.Sprint(q["address"]) != fmt.Sprint([]string{child}) {
					t.Fatal(q)
				}
				if tc.name != "unprocessed-child" {
					return []filter.Object{}, nil
				}
				log["blockNumber"], log["blockHash"], log["address"], log["topics"], log["data"] = "0x7", sparseRangeHead(1, 7).Hash, child, []any{abi.EventSelector(ping)}, "0x"+strings.Repeat("0", 64)
				return []filter.Object{log}, nil
			})
			width := uint64(20)
			reader := LogScanRPC{Actions: rpc.NewActions(node), Options: rpc.ActionOptions{EthGetLogsBlockRange: &width}}
			got, err := reader.scan(t.Context(), opts, logScanRange{chain: 1, from: 6, to: 8, emptyFrom: 6, checkEmpty: true}, func(chain, n uint64) (ManagedLiveHead, error) { return sparseRangeHead(chain, n), nil })
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			if err != nil {
				if len(got.Coverage) != 0 || got.FiltersHash != "" {
					t.Fatal("partial proof", got)
				}
				return
			}
			if !slices.Equal(requests, []string{"0x1-0x2", "0x6-0x8"}) {
				t.Fatal(requests)
			}
			if err = validateLogCoverage(group, got.Coverage, 6, 8); err != nil {
				t.Fatal(err)
			}
			got.Coverage[0][1].Intervals = []SourceInterval{{2, 2}}
			if err = validateLogCoverage(group, got.Coverage, 6, 8); !errors.Is(err, ErrManagedLivePendingActions) {
				t.Fatal("missing birth prefix", err)
			}
		})
	}
}

func TestCachedLogFactoryEmitter(t *testing.T) {
	address := "0x" + strings.Repeat("11", 20)
	other := "0x" + strings.Repeat("22", 20)
	item := abi.Event{Type: "event", Name: "Ping"}
	factory := filter.Object{"id": "children"}
	f := filter.Object{"type": "log", "chainId": 1, "fromBlock": 1, "toBlock": 5, "address": factory, "topic0": abi.EventSelector(item)}
	children := logFactoryChildren{"children": {{Address: address, Block: 3}}}
	for _, tc := range []struct {
		name, emitter   string
		height          uint64
		static, pending bool
	}{
		{"before-birth", address, 2, false, false}, {"unregistered", other, 4, false, false},
		{"birth-block", address, 3, false, true}, {"overlapping-static", other, 4, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := []filter.Object{f}
			abis := []any{item}
			if tc.static {
				static := maps.Clone(f)
				static["address"] = other
				group = append(group, static)
				abis = append(abis, item)
			}
			callbacks, err := logOnlyABICallbacks(map[uint64][]filter.Object{1: group}, map[uint64][]any{1: abis})
			if err != nil {
				t.Fatal(err)
			}
			log := filter.Object{"blockNumber": tc.height, "transactionIndex": 0, "logIndex": 0, "address": tc.emitter, "topics": []any{abi.EventSelector(item)}, "data": "0x"}
			rows := SourceEventData{Blocks: []filter.Object{{"number": tc.height, "timestamp": 17}}, Logs: []filter.Object{log}}
			err = verifyABISkippedPage(1, callbacks[1], rows, children)
			if tc.pending && !errors.Is(err, ErrManagedLivePendingActions) || !tc.pending && err != nil {
				t.Fatalf("pending=%v error=%v", tc.pending, err)
			}
		})
	}
}
