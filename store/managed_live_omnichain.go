package store

import "ponder.local/ponder-go/schema"

// Only the RuntimeRunner constructor enables this separate on-disk format.
// Legacy finite, recovery and live-domain APIs continue to require multichain.
func managedStorageOrdering(opts *ManagedLiveOptions) schema.Ordering {
	if opts != nil && opts.runtimeOmnichain {
		return schema.Omnichain
	}
	return schema.Multichain
}

func omnichainSafeID(batch string) string { return "omni-safe-" + managedHash(batch)[:48] }
