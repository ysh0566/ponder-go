package store

import (
	"context"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/schema"
)

const managedLiveRuntimeVersion = "managed-live-runtime-v5"
const managedLiveOmnichainVersion = "managed-live-omnichain-finalized-v11"
const managedLiveContinuousVersion = "managed-live-continuous-v6"
const managedLiveUncachedVersion = "managed-live-uncached-v7"
const managedLiveFactoryVersion = "managed-live-factory-v10"
const managedLiveSetupVersion = "managed-live-setup-v8"

func isLiveRuntimeVersion(v string) bool {
	return v == managedLiveRuntimeVersion || v == managedLiveOmnichainVersion || logOnlyVersion(v) || isContinuousRuntimeVersion(v)
}

func isContinuousRuntimeVersion(v string) bool {
	return v == managedLiveContinuousVersion || v == managedLiveUncachedVersion || v == managedLiveSetupVersion || v == managedLiveSourceVersion || v == managedLiveFactoryVersion
}

type ManagedLiveRuntimeOptions struct {
	ManagedLiveOptions
	StartBlocks map[uint64]uint64
	// Nil means all chains cached. A complete mixed/disabled policy opts into v7.
	CacheRPCRequests  map[uint64]bool
	Continuous        bool
	Setup             bool
	SourceCheckpoints bool
	LogOnlyFilters    map[uint64][]filter.Object
	// Event ABI items in the same order as LogOnlyFilters, for proving ABI-skipped ranges.
	LogOnlyEventABIs map[uint64][]any
	// OpenLogScanRPC supplies fresh transports for uncached chains during a proof scan.
	OpenLogScanRPC func(context.Context) (map[uint64]LogScanRPC, error)
	FactorySources bool
	ObserveDomain  func(context.Context, ManagedLiveDomainStage) error
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
	if o.Ordering == schema.Omnichain {
		if in.Continuous || !in.SourceCheckpoints || in.ObserveDomain != nil {
			return nil, fmt.Errorf("omnichain requires the bounded finalized runtime format")
		}
		for _, chain := range in.Chains {
			cp, err := checkpoint.Decode(in.FinalizedHeads[chain.ID].Checkpoint)
			if err != nil || cp.ChainID != chain.ID || cp.BlockNumber < in.EndBlocks[chain.ID] {
				return nil, fmt.Errorf("omnichain runtime end exceeds acquired finality")
			}
		}
		in.ManagedLiveOptions.runtimeOmnichain = true
	}
	opts, err := normalizeManagedLiveOptions(o, in.ManagedLiveOptions)
	if err != nil {
		return nil, err
	}
	opts.recovery = true
	opts.coordinated = true
	opts.liveRuntime = true
	opts.runtimeSetup = in.Setup
	opts.runtimeInitial = in.SourceCheckpoints
	if in.FactorySources && !in.SourceCheckpoints {
		return nil, fmt.Errorf("factory runtime requires source checkpoints")
	}
	opts.runtimeFactories = in.FactorySources
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
			opts.runtimeCachePolicy = copied
		}
	}
	// Default-ordering deployments share authenticated recovery rules across
	// fixed ranges and continuously polled execution.
	opts.liveContinuous = !opts.runtimeOmnichain && (in.Continuous || opts.runtimeSetup || opts.runtimeCachePolicy != nil || opts.runtimeInitial)
	if len(in.StartBlocks) != len(opts.Chains) {
		return nil, fmt.Errorf("runtime requires exact start blocks")
	}
	opts.runtimeStarts = map[uint64]uint64{}
	for _, chain := range opts.Chains {
		start, ok := in.StartBlocks[chain.ID]
		if !ok || (!opts.runtimeInitial && start == 0) || start > opts.EndBlocks[chain.ID] {
			return nil, fmt.Errorf("invalid start block for chain %d: require a start at or before the configured end and source checkpoints for block zero", chain.ID)
		}
		opts.runtimeStarts[chain.ID] = start
	}

	if in.LogOnlyFilters != nil {
		opts.runtimeLogFilters, err = normalizeLogOnlyFilters(in.LogOnlyFilters, opts)
		if err != nil {
			return nil, err
		}
	}
	if in.LogOnlyEventABIs != nil {
		opts.runtimeLogCallbacks, err = logOnlyABICallbacks(opts.runtimeLogFilters, in.LogOnlyEventABIs)
		if err != nil {
			return nil, err
		}
	}
	needsRPCScan := logOnlyOptions(opts) && opts.runtimeCachePolicy != nil
	if needsRPCScan && in.OpenLogScanRPC == nil {
		return nil, fmt.Errorf("uncached log-only mode requires its own scan RPC factory")
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
	d := &ManagedLiveDomains{storage: s, spec: liveDomainSpec(o, opts), openLogScanRPC: in.OpenLogScanRPC}
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
