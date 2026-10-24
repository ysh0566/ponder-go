package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

type LogRange struct {
	FromBlock string `json:"fromBlock"`
	ToBlock   string `json:"toBlock"`
}
type LogsRetryPlan struct {
	ShouldRetry      bool
	IsSuggestedRange bool
	Ranges           []LogRange
}

func (p LogsRetryPlan) MarshalJSON() ([]byte, error) {
	if !p.ShouldRetry {
		return json.Marshal(map[string]any{"shouldRetry": false})
	}
	return json.Marshal(map[string]any{"shouldRetry": true, "isSuggestedRange": p.IsSuggestedRange, "ranges": p.Ranges})
}

type logsRetryRule struct {
	pattern *regexp.Regexp
	kind    string
	group   int
	fixed   int64
}

func rule(pattern, kind string, group int, fixed int64) logsRetryRule {
	return logsRetryRule{regexp.MustCompile(pattern), kind, group, fixed}
}

var logsRetryRules = []logsRetryRule{
	rule(`requested too many blocks from (\d+) to (\d+), maximum is set to (\d+)`, "minus_one", 3, 0),
	rule(`Max range: (\d+)`, "minus_one", 1, 0),
	rule(`Maximum allowed number of requested blocks is ([\d]+)`, "decimal", 1, 0),
	rule(`Try with this block range \[0x([0-9a-fA-F]+),\s*0x([0-9a-fA-F]+)\]`, "hex_span", 0, 0),
	rule(`block range is too wide`, "ankr", 0, 3000),
	rule(`this block range should work: \[0x([0-9a-fA-F]+),\s*0x([0-9a-fA-F]+)\]`, "hex_span", 0, 0),
	rule(`limited to a ([\d,.]+)`, "decimal", 1, 0),
	rule(`limited to ([\d,.]+) block`, "decimal", 1, 0),
	rule(`exceeds the range allowed for your plan \(\d+ > (\d+)\)`, "decimal", 1, 0),
	rule(`Block range limit exceeded.`, "chainstack", 0, 0),
	rule(`please limit the query to at most ([\d,.]+) blocks`, "minus_one", 1, 0),
	rule(`maximum block range: ([\d,.]+)`, "decimal", 1, 0),
	rule(`query exceeds max block range ([\d,.]+)`, "decimal", 1, 0),
	rule(`block range greater than ([\d,.]+) max`, "decimal", 1, 0),
	rule(`block range exceeds ([\d,.]+)`, "decimal", 1, 0),
	rule(`eth_getLogs range is too large, max is 10k blocks`, "fixed", 0, 10000),
	rule(`eth_getLogs range is too large, max is 1k blocks`, "fixed", 0, 1000),
	rule(`query must be smaller than size ([\d,.]+)`, "decimal", 1, 0),
	rule(`block range is too wide \(maximum (\d+)\)`, "decimal", 1, 0),
	rule(`up to a ([\d,.]+) block range`, "decimal", 1, 0),
	rule(`maximum \[from, to\] blocks distance: (\d+)`, "decimal", 1, 0),
	rule(`exceed max block range: (\d+)`, "minus_one", 1, 0),
}
var logsRetryFallback = regexp.MustCompile(`logs matched by query exceeds limit of \d+|query returned more than \d+ results|response size should not greater than \d+ bytes|ranges over \d+ blocks are not supported on freetier`)
var logsRetrySubstrings = []string{"allowed block range threshold exceeded", "exceeded max allowed", "range threshold exceeded", "no backend is currently healthy to serve traffic", "block range too large", "Block range is too large", "backend response too large", "query exceeds max results"}

// GetLogsRetryHelper preserves the source's inclusive delta, provider ordering,
// JSON error matching and exact hexadecimal unchanged-range comparison.
func GetLogsRetryHelper(query map[string]any, failure any) (LogsRetryPlan, error) {
	from, err := rpcQuantity(query["fromBlock"])
	if err != nil {
		return LogsRetryPlan{}, err
	}
	to, err := rpcQuantity(query["toBlock"])
	if err != nil {
		return LogsRetryPlan{}, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(failure); err != nil {
		return LogsRetryPlan{}, err
	}
	serialized := buffer.String()
	var fields map[string]any
	_ = json.Unmarshal(buffer.Bytes(), &fields)
	plan := func(delta *big.Int, suggested bool) (LogsRetryPlan, bool, error) {
		if delta.Sign() < 0 {
			return LogsRetryPlan{}, false, nil
		} // Never loop on an invalid zero-width provider hint.
		one := big.NewInt(1)
		step := new(big.Int).Add(delta, one)
		ranges := []LogRange{}
		for start := new(big.Int).Set(from); start.Cmp(to) <= 0; start.Add(start, step) {
			end := new(big.Int).Add(start, delta)
			if end.Cmp(to) > 0 {
				end.Set(to)
			}
			ranges = append(ranges, LogRange{"0x" + start.Text(16), "0x" + end.Text(16)})
		}
		if len(ranges) == 0 || len(ranges) == 1 && ranges[0].FromBlock == query["fromBlock"] && ranges[0].ToBlock == query["toBlock"] {
			return LogsRetryPlan{}, false, nil
		}
		return LogsRetryPlan{true, suggested, ranges}, true, nil
	}
	for _, r := range logsRetryRules {
		match := r.pattern.FindStringSubmatch(serialized)
		if match == nil {
			continue
		}
		var delta *big.Int
		switch r.kind {
		case "fixed":
			delta = big.NewInt(r.fixed)
		case "ankr":
			if fields["code"] != float64(-32600) {
				continue
			}
			delta = big.NewInt(r.fixed)
		case "chainstack":
			delta = big.NewInt(10000)
			if new(big.Int).Sub(to, from).Cmp(delta) < 0 {
				delta = big.NewInt(100)
			}
		case "hex_span":
			a, ok := new(big.Int).SetString(match[1], 16)
			if !ok {
				return LogsRetryPlan{}, fmt.Errorf("invalid provider range")
			}
			b, ok := new(big.Int).SetString(match[2], 16)
			if !ok {
				return LogsRetryPlan{}, fmt.Errorf("invalid provider range")
			}
			delta = new(big.Int).Sub(b, a)
		default:
			var ok bool
			delta, ok = new(big.Int).SetString(strings.NewReplacer(",", "", ".", "").Replace(match[r.group]), 10)
			if !ok {
				return LogsRetryPlan{}, fmt.Errorf("invalid provider range")
			}
			if r.kind == "minus_one" {
				delta.Sub(delta, big.NewInt(1))
			}
		}
		if result, changed, e := plan(delta, true); e != nil {
			return result, e
		} else if changed {
			return result, nil
		}
	}
	fallback := logsRetryFallback.MatchString(serialized)
	for _, text := range logsRetrySubstrings {
		fallback = fallback || strings.Contains(serialized, text)
	}
	if fallback {
		delta := new(big.Int).Div(new(big.Int).Sub(to, from), big.NewInt(2))
		if result, changed, e := plan(delta, false); e != nil {
			return result, e
		} else if changed {
			return result, nil
		}
	}
	return LogsRetryPlan{}, nil
}
