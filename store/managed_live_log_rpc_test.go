package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/rpc"
)

type logScanTestRPC func(map[string]any) ([]filter.Object, error)

func (f logScanTestRPC) Call(_ context.Context, method string, params, out any) error {
	if method != "eth_getLogs" {
		return fmt.Errorf("unexpected RPC method %s", method)
	}
	rows, err := f(params.([]any)[0].(map[string]any))
	*out.(*[]map[string]any) = rows
	return err
}

func TestUncachedLogScanEvidence(t *testing.T) {
	item := abi.Event{Name: "Value", Type: "event", Inputs: []abi.Parameter{{Name: "n", Type: "uint256"}}}
	indexed := item
	indexed.Inputs = []abi.Parameter{{Name: "n", Type: "uint256", Indexed: true}}
	selector := abi.EventSelector(item)
	address := "0x" + strings.Repeat("ab", 20)
	f := filter.Object{"type": "log", "chainId": 1, "fromBlock": 2, "toBlock": 3, "topic0": selector, "address": address}
	filters := map[uint64][]filter.Object{1: {f, f}}
	callbacks, e := logOnlyABICallbacks(filters, map[uint64][]any{1: {item, indexed}})
	if e != nil {
		t.Fatal(e)
	}
	opts := ManagedLiveOptions{runtimeLogFilters: filters, runtimeLogCallbacks: callbacks}
	word := "0x" + strings.Repeat("0", 63) + "1"
	failedPage := errors.New("RPC unavailable after an empty page")
	for _, tc := range []struct {
		name string
		want error
	}{
		{"both_ABIs_skip", nil}, {"later_ABI_decodes", ErrManagedLivePendingActions}, {"wrong_hash", ErrManagedLiveBatchConflict}, {"outside_page", ErrManagedLiveBatchConflict}, {"wrong_address", ErrManagedLiveBatchConflict}, {"removed", ErrManagedLiveBatchConflict}, {"last_page_fails", failedPage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var queried []string
			node := logScanTestRPC(func(q map[string]any) ([]filter.Object, error) {
				if q["address"] != address || q["fromBlock"] != q["toBlock"] {
					t.Fatalf("unexpected query %v", q)
				}
				n := fmt.Sprint(q["fromBlock"])
				queried = append(queried, n)
				if n == "0x2" {
					return []filter.Object{}, nil
				}
				if n != "0x3" {
					t.Fatalf("queried outside filter range %s", n)
				}
				log := filter.Object{"blockNumber": n, "logIndex": "0x0", "blockHash": sparseRangeHead(1, 3).Hash, "address": address, "topics": []any{selector, "0x02"}, "data": "0x01"}
				switch tc.name {
				case "later_ABI_decodes":
					if len(queried) > 2 {
						return []filter.Object{}, nil
					}
					log["topics"] = []any{selector, word}
				case "wrong_hash":
					log["blockHash"] = sparseRangeHead(1, 2).Hash
				case "outside_page":
					log["blockNumber"] = "0x2"
				case "wrong_address":
					log["address"] = "0x" + strings.Repeat("cd", 20)
				case "removed":
					log["removed"] = true
				case "last_page_fails":
					return nil, failedPage
				}
				return []filter.Object{log}, nil
			})
			limit := uint64(1)
			reader := LogScanRPC{Actions: rpc.NewActions(node), Options: rpc.ActionOptions{EthGetLogsBlockRange: &limit}}
			scan, err := reader.scan(t.Context(), opts, logScanRange{chain: 1, from: 1, to: 4, checkEmpty: true, emptyFrom: 2}, func(chain, n uint64) (ManagedLiveHead, error) { return sparseRangeHead(chain, n), nil })
			if !errors.Is(err, tc.want) {
				t.Fatalf("scan error=%v want=%v", err, tc.want)
			}
			if err != nil {
				if len(scan.Coverage) != 0 || scan.FiltersHash != "" {
					t.Fatalf("partial proof returned after error: %+v", scan)
				}
				return
			}
			if !slices.Equal(queried, []string{"0x2", "0x3", "0x2", "0x3"}) || scan.FiltersHash != managedHash(callbacks[1]) {
				t.Fatalf("queries=%v digest=%s", queried, scan.FiltersHash)
			}
			if err := validateLogCoverage(filters[1], scan.Coverage, 1, 4); err != nil {
				t.Fatal(err)
			}
		})
	}
}
