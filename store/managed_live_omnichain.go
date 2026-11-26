package store

import "github.com/ysh0566/ponder-go/schema"

// These finalized modes commit each batch and its safe receipt together.
func (o ManagedLiveOptions) atomicFinality() bool {
	return o.runtimeOmnichain || (o.runtimeIsolated && !o.liveContinuous)
}

// Only the RuntimeRunner constructor enables this separate on-disk format.
// Legacy finite, recovery and live-domain APIs continue to require multichain.
func managedStorageOrdering(opts *ManagedLiveOptions) schema.Ordering {
	if opts != nil && opts.runtimeOmnichain {
		return schema.Omnichain
	}
	if opts != nil && opts.runtimeIsolated {
		return schema.Isolated
	}
	return schema.Multichain
}

func atomicSafeID(version, batch string) string {
	prefix := "omni-safe-"
	if version == managedLiveIsolatedVersion {
		prefix = "isolated-safe-"
	}
	return prefix + managedHash(batch)[:48]
}
