package store

import "encoding/json"

// Legacy formats retain their strict field set, including rejection of explicit
// zero-valued v5 extensions. New lifecycle fields are accepted only in v5.
func (c *ManagedLiveControl) UnmarshalJSON(raw []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	var version string
	if err := json.Unmarshal(object["version"], &version); err != nil {
		return err
	}
	if !isLiveRuntimeVersion(version) {
		for _, key := range []string{"serving", "activations", "bootstraps"} {
			if _, exists := object[key]; exists {
				return ErrManagedLiveBatchConflict
			}
		}
	}
	type plain ManagedLiveControl
	var value plain
	if err := decodeLiveJSON(raw, &value); err != nil {
		return err
	}
	*c = ManagedLiveControl(value)
	return nil
}
