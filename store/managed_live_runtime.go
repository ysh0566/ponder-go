package store

import (
	"context"
	"fmt"
)

const managedLiveRuntimeVersion = "managed-live-runtime-v5"
const managedLiveContinuousVersion = "managed-live-continuous-v6"
const managedLiveUncachedVersion = "managed-live-uncached-v7"
const managedLiveSetupVersion = "managed-live-setup-v8"

func isLiveRuntimeVersion(v string) bool {
	return v == managedLiveRuntimeVersion || isContinuousRuntimeVersion(v)
}

func isContinuousRuntimeVersion(v string) bool {
	return v == managedLiveContinuousVersion || v == managedLiveUncachedVersion || v == managedLiveSetupVersion || v == managedLiveSourceVersion
}

type ManagedLiveRuntimeOptions struct {
	ManagedLiveOptions
	StartBlocks map[uint64]uint64
	// Nil means all chains cached. A complete mixed/disabled policy opts into v7.
	CacheRPCRequests  map[uint64]bool
	Continuous        bool
	Setup             bool
	SourceCheckpoints bool
	ObserveDomain     func(context.Context, ManagedLiveDomainStage) error
}

// Runtime constructors isolate the active lifecycle format from domains-v4.
func OpenManagedLiveRuntimePostgres(ctx context.Context, o IndependentStorageOptions, in ManagedLiveRuntimeOptions) (*ManagedLiveDomains, error) {
	return openManagedLiveRuntime(ctx, o, in, true)
}
func OpenManagedLiveRuntimePGlite(ctx context.Context, o IndependentStorageOptions, in ManagedLiveRuntimeOptions) (*ManagedLiveDomains, error) {
	return openManagedLiveRuntime(ctx, o, in, false)
}
func openManagedLiveRuntime(ctx context.Context, o IndependentStorageOptions, in ManagedLiveRuntimeOptions, pg bool) (*ManagedLiveDomains, error) {
	if in.ObserveDomain != nil && !in.Continuous {
		return nil, fmt.Errorf("domain observer requires continuous-v6")
	}
	opts, err := normalizeManagedLiveOptions(o, in.ManagedLiveOptions)
	if err != nil {
		return nil, err
	}
	opts.recovery = true
	opts.coordinated = true
	opts.liveRuntime = true
	opts.liveContinuous = in.Continuous
	if in.Setup && !in.Continuous {
		return nil, fmt.Errorf("setup runtime requires continuous-v8 lifecycle")
	}
	opts.runtimeSetup = in.Setup
	if in.SourceCheckpoints && !in.Continuous {
		return nil, fmt.Errorf("source checkpoints require the v9 live lifecycle")
	}
	opts.runtimeInitial = in.SourceCheckpoints
	if in.CacheRPCRequests != nil {
		if len(in.CacheRPCRequests) != len(opts.Chains) {
			return nil, fmt.Errorf("runtime requires exact RPC cache policy")
		}
		copied := map[uint64]bool{}
		disabled := false
		for _, chain := range opts.Chains {
			enabled, ok := in.CacheRPCRequests[chain.ID]
			if !ok {
				return nil, fmt.Errorf("runtime RPC cache chain missing")
			}
			copied[chain.ID] = enabled
			disabled = disabled || !enabled
		}
		if disabled {
			if !in.Continuous {
				return nil, fmt.Errorf("uncached runtime requires continuous-v7 lifecycle")
			}
			opts.runtimeCachePolicy = copied
		}
	}
	if len(in.StartBlocks) != len(opts.Chains) {
		return nil, fmt.Errorf("runtime requires exact start blocks")
	}
	opts.runtimeStarts = map[uint64]uint64{}
	for _, chain := range opts.Chains {
		start, ok := in.StartBlocks[chain.ID]
		if !ok || (!opts.runtimeInitial && start == 0) || start > opts.EndBlocks[chain.ID] {
			return nil, fmt.Errorf("first runtime closure requires start>=1 and start<=finite end")
		}
		opts.runtimeStarts[chain.ID] = start
	}

	if !pg && (o.BusinessDirectory == "" || o.SyncDirectory == "" || o.BusinessDirectory == "memory://" || o.SyncDirectory == "memory://") {
		return nil, fmt.Errorf("managed runtime requires persistent directories")
	}
	base := &managedStorageOptions{ManagedFiniteOptions: opts.ManagedFiniteOptions, live: &opts}
	var s *IndependentStorage
	if pg {
		s, err = openIndependentPostgres(ctx, o, base)
	} else {
		s, err = openIndependentPGlite(ctx, o, base)
	}
	if err != nil {
		return nil, fmt.Errorf("runtime native open: %w", err)
	}
	d := &ManagedLiveDomains{storage: s, spec: liveDomainSpec(o, opts)}
	if err = d.Resume(ctx); err != nil {
		_ = s.Close(context.Background())
		return nil, fmt.Errorf("runtime resume: %w", err)
	}
	if err = d.ensureRuntimeFence(ctx); err != nil {
		_ = s.Close(context.Background())
		return nil, fmt.Errorf("runtime sync fence: %w", err)
	}
	d.domainObserver = in.ObserveDomain
	return d, nil
}
