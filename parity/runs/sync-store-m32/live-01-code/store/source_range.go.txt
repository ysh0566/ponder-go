package store

import "math"

// SourceFilterRange mirrors the nullish coalescing bounds of the source helper.
// Float64 preserves the source number domain; native unsigned scheduler bounds
// remain separate. This standalone helper is not a complete event query planner.
type SourceFilterRange struct{ FromBlock, ToBlock *float64 }
type SourceBlockRange struct{ FromBlock, ToBlock float64 }

func GetSourceFilterBlockRange(filters []SourceFilterRange, fromBlock, toBlock float64) SourceBlockRange {
	if len(filters) == 0 {
		return SourceBlockRange{fromBlock, toBlock}
	}
	from, to := math.Inf(1), math.Inf(-1)
	for _, f := range filters {
		low, high := fromBlock, toBlock
		if f.FromBlock != nil {
			low = *f.FromBlock
		}
		if f.ToBlock != nil {
			high = *f.ToBlock
		}
		from = math.Min(from, math.Max(fromBlock, low))
		to = math.Max(to, math.Min(toBlock, high))
	}
	return SourceBlockRange{from, to}
}
