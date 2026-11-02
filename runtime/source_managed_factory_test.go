package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"ponder.local/ponder-go/filter"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
)

type managedFactoryProvider struct {
	mode     string
	large    bool
	calls    int
	maxLive  uint64
	baseline uint64
}

func managedFactoryFilter() filter.Object {
	return filter.Object{"type": "log", "id": "factory-a", "chainId": uint64(1), "address": "0x1111111111111111111111111111111111111111", "eventSelector": "0x" + strings.Repeat("a", 64), "childAddressLocation": "topic1", "fromBlock": uint64(1), "toBlock": uint64(3)}
}
func (p *managedFactoryProvider) Call(_ context.Context, m string, params, out any) error {
	p.calls++
	if p.mode == "transport" {
		return errors.New("controlled acquisition transport failure")
	}
	var result any
	switch m {
	case "eth_getBlockByHash":
		h := fmt.Sprint(params.([]any)[0])
		n, e := strconv.ParseUint(strings.TrimPrefix(h, "0x"), 16, 64)
		if e != nil {
			return e
		}
		b := realtimeControlBlock(n)
		if p.mode == "wrong-root" && n == 4 {
			b["hash"] = fmt.Sprintf("0x%064x", 99)
		}
		if p.mode == "wrong-parent" && n == 2 {
			b["number"] = "0x7"
		}
		if p.large {
			b["extraData"] = "0x" + strings.Repeat("ab", 128*1024)
			if p.calls%100 == 0 {
				goruntime.GC()
				var s goruntime.MemStats
				goruntime.ReadMemStats(&s)
				if s.HeapAlloc > p.maxLive {
					p.maxLive = s.HeapAlloc
				}
			}
		}
		result = b
	case "eth_getLogs":
		f := params.([]any)[0].(map[string]any)
		n, _ := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(f["blockHash"]), "0x"), 16, 64)
		logs := []filter.Object{}
		if n == 1 || n == 3 {
			child := "2"
			if n == 3 {
				child = "3"
			}
			b := realtimeControlBlock(n)
			logs = append(logs, filter.Object{"address": managedFactoryFilter()["address"], "blockHash": b["hash"], "blockNumber": b["number"], "transactionHash": "0x" + strings.Repeat("0", 64), "transactionIndex": "0x0", "logIndex": "0x0", "removed": false, "topics": []any{managedFactoryFilter()["eventSelector"], "0x" + strings.Repeat("0", 24) + strings.Repeat(child, 40)}, "data": "0x"})
		}
		result = logs
	default:
		return fmt.Errorf("unexpected factory reconstruction RPC %s", m)
	}
	b, e := json.Marshal(result)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func TestManagedFactoryBranchRestore(t *testing.T) {
	for _, mode := range []string{"valid", "transport", "wrong-root", "wrong-parent"} {
		t.Run(mode, func(t *testing.T) {
			f := managedFactoryFilter()
			children := NewSourceHistoricalChildren([]SourceFactoryChildren{{ID: "factory-a"}})
			p := &managedFactoryProvider{mode: mode}
			s, e := RestoreManagedFactoryBranch(context.Background(), p, children, realtimeControlBlock(2), []filter.Object{realtimeControlBlock(3), realtimeControlBlock(4)}, []filter.Object{{"type": "log", "address": f, "fromBlock": uint64(1), "toBlock": uint64(5)}}, SourceRealtimeChain{ID: 1})
			if mode != "valid" {
				if e == nil {
					t.Fatal("bad branch/transport accepted")
				}
				if (mode == "transport") != errors.Is(e, ErrManagedFactoryAcquisition) {
					t.Fatalf("error classification %v", e)
				}
				return
			}
			got, _ := children.Entries("factory-a")
			if len(got) != 2 || got[0].Block != 1 || got[1].Block != 3 {
				t.Fatalf("birth projection %v", got)
			}
			if len(s.childrenPerBlock) != 2 {
				t.Fatalf("retained birth provenance %v", s.childrenPerBlock)
			}
			replacement := realtimeControlBlock(3)
			replacement["hash"] = fmt.Sprintf("0x%064x", 99)
			ev, e := s.reconcileReorg(context.Background(), replacement)
			if e != nil || ev.Type != "reorg" {
				t.Fatal(ev, e)
			}
			got, _ = children.Entries("factory-a")
			if len(got) != 1 || got[0].Block != 1 {
				t.Fatalf("orphan birth survived or safe birth lost: %v", got)
			}
		})
	}
}
func TestManagedFactoryLargePrefix(t *testing.T) {
	goruntime.GC()
	var start goruntime.MemStats
	goruntime.ReadMemStats(&start)
	p := &managedFactoryProvider{large: true, baseline: start.HeapAlloc}
	children := NewSourceHistoricalChildren([]SourceFactoryChildren{{ID: "factory-a"}})
	s, e := RestoreManagedFactoryBranch(context.Background(), p, children, realtimeControlBlock(512), nil, []filter.Object{{"type": "log", "address": managedFactoryFilter(), "fromBlock": uint64(1), "toBlock": uint64(600)}}, SourceRealtimeChain{ID: 1})
	if e != nil {
		t.Fatal(e)
	}
	if len(s.childrenPerBlock) != 0 || len(s.unfinalized) != 0 {
		t.Fatal("finalized historical provenance retained")
	}
	// 512*256KiB (>128MiB) full RPC payloads may not remain reachable. The
	// generous 32MiB headroom covers ordinary decoder/Go test allocations.
	if p.maxLive > start.HeapAlloc+32*1024*1024 {
		t.Fatalf("full historical blocks retained: baseline%d max%d", start.HeapAlloc, p.maxLive)
	}
	t.Logf("prefix512 payload256KiB calls%d baseline_heap%d sampled_peak_live%d", p.calls, start.HeapAlloc, p.maxLive)
}

func TestManagedFactoryPinnedGoldens(t *testing.T) {
	raw, e := os.ReadFile("../testdata/runtime/managed-factory-17c6d5ba.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Name  string
		Input map[string]any
		Value any
		Error *struct{ Name, Message string }
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	for _, tc := range cases {
		if !strings.HasPrefix(tc.Name, "extract_") && !strings.HasPrefix(tc.Name, "factory_birth_bounds_") && !strings.HasPrefix(tc.Name, "child_membership_") && tc.Name != "effective_filter_bounds" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			switch {
			case strings.HasPrefix(tc.Name, "extract_"):
				location, _ := tc.Input["location"].(string)
				log, _ := tc.Input["log"].(map[string]any)
				if location == "" {
					switch tc.Name {
					case "extract_data_parameter":
						location = "offset32"
					case "extract_short_data", "extract_missing_data":
						location = "offset0"
					default:
						location = "topic1"
					}
				}
				if tc.Name == "extract_topic_case_preserved" {
					log = map[string]any{"topics": []any{"0x", tc.Input["topic"]}}
				}
				got, err := filter.GetSourceChildAddress(log, filter.Object{"childAddressLocation": location})
				if tc.Error != nil {
					if err == nil || err.Error() != tc.Error.Message {
						t.Fatalf("actual pinned error got=%v want=%+v", err, tc.Error)
					}
					return
				}
				if err != nil || got != tc.Value {
					t.Fatalf("actual pinned extraction got=%v err=%v want=%v", got, err, tc.Value)
				}
			case strings.HasPrefix(tc.Name, "factory_birth_bounds_"):
				f := managedFactoryFilter()
				f["fromBlock"] = tc.Input["from"]
				f["toBlock"] = tc.Input["to"]
				log := filter.Object{"address": f["address"], "topics": []any{f["eventSelector"]}, "blockNumber": Hex(uint64(tc.Input["number"].(float64)))}
				if got := filter.IsLogFactoryMatched(f, log); got != tc.Value {
					t.Fatalf("actual pinned factory bounds got%v want%v", got, tc.Value)
				}
			case strings.HasPrefix(tc.Name, "child_membership_"):
				addr := tc.Input["address"].(string)
				got := filter.IsAddressMatched(addr, tc.Input["eventBlock"].(float64), []filter.ChildAddress{{Address: addr, Block: uint64(tc.Input["birthBlock"].(float64))}})
				if got != tc.Value {
					t.Fatalf("actual pinned child birth got%v want%v", got, tc.Value)
				}
			default:
				f := filter.Object{"type": "log", "fromBlock": tc.Input["childFrom"], "toBlock": tc.Input["childTo"], "address": filter.Object{"type": "log", "fromBlock": tc.Input["factoryFrom"], "toBlock": tc.Input["factoryTo"]}}
				want := tc.Value.(map[string]any)
				if filter.FilterFromBlock(f) != want["from"] || filter.FilterToBlock(f) != want["to"] {
					t.Fatalf("actual pinned effective bounds differ %v", f)
				}
			}
		})
	}
}
