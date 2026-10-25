package runtime

import "fmt"

// GetSourceOmnichainCheckpoint ports the source runtime's cross-chain cursor
// aggregation. Progress objects retain their identities and must not be mutated
// concurrently with this call. An unset end on any chain keeps the global end
// unset. Completed chains are excluded only from the current minimum.
func GetSourceOmnichainCheckpoint(progress []*SourceSyncProgress, tag SourceSyncProgressTag) (*string, error) {
	if len(progress) == 0 {
		// The source's min/max reduce without an initial accumulator also throws.
		return nil, fmt.Errorf("source omnichain checkpoint requires at least one chain")
	}
	checkpoints := make([]*string, len(progress))
	for i, p := range progress {
		if p == nil {
			return nil, fmt.Errorf("source omnichain sync progress required")
		}
		cp, err := p.GetCheckpoint(tag)
		if err != nil {
			return nil, err
		}
		checkpoints[i] = cp
	}
	maximum := tag == SourceSyncEnd
	complete := make([]bool, len(progress))
	if tag == SourceSyncEnd {
		for _, cp := range checkpoints {
			if cp == nil {
				return nil, nil
			}
		}
	}
	if tag == SourceSyncCurrent {
		maximum = true
		for i, p := range progress {
			var err error
			complete[i], err = p.IsEnd()
			if err != nil {
				return nil, err
			}
			maximum = maximum && complete[i]
		}
	}
	var result *string
	for i, cp := range checkpoints {
		if tag == SourceSyncCurrent && !maximum && complete[i] {
			continue
		}
		if cp == nil {
			continue
		}
		if result == nil || (maximum && *cp > *result) || (!maximum && *cp < *result) {
			value := *cp
			result = &value
		}
	}
	return result, nil
}
