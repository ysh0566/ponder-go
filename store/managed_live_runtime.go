package store

import (
	"context"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/schema"
)

const managedLiveRuntimeVersion = "managed-live-runtime-v5"
const managedLiveIsolatedContinuousVersion = "managed-live-isolated-continuous-v1"
const managedLiveIsolatedVersion = "managed-live-isolated-finalized-v1"
const managedLiveOmnichainVersion = "managed-live-omnichain-finalized-v11"
const managedLiveContinuousVersion = "managed-live-continuous-v6"
const managedLiveUncachedVersion = "managed-live-uncached-v7"
const managedLiveFactoryVersion = "managed-live-factory-v10"
const managedLiveSetupVersion = "managed-live-setup-v8"

func isLiveRuntimeVersion(v string) bool {
	return v == managedLiveRuntimeVersion || v == managedLiveIsolatedVersion || v == managedLiveOmnichainVersion || logOnlyVersion(v) || isContinuousRuntimeVersion(v)
}

func isContinuousRuntimeVersion(v string) bool {
	return v == managedLiveIsolatedContinuousVersion || v == managedLiveContinuousVersion || v == managedLiveUncachedVersion || v == managedLiveSetupVersion || v == managedLiveSourceVersion || v == managedLiveFactoryVersion
}

type ManagedLiveRuntimeOptions struct {
	ManagedLiveOptions
	StartBlocks map[uint64]uint64
	// MaxQueryParameters bounds batched sync writes for this opening.
	MaxQueryParameters int
	// Nil means all chains cached. A complete mixed/disabled policy opts into v7.
	CacheRPCRequests map[uint64]bool
	// ResumeUnavailable permits missing RPC heads only when both stores prove
	// an initialized Isolated deployment. Recovery reuses each absent chain's
	// finalized business position. The runner authenticates it on reconnect.
	ResumeUnavailable bool
	Continuous        bool
	Setup             bool
	SourceCheckpoints bool
	// Filters for chains without a covering block callback. Logs, contract CALLs,
	// account transactions and transfers are checked against the sync store or a fresh per-chain RPC reader.
	// List each chain, using nil for a chain with a covering block callback.
	LogOnlyFilters map[uint64][]filter.Object
	// ABI entries follow filter order. Account entries have no ABI.
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
	budget, err := SourceQueryParameterBudget(in.MaxQueryParameters)
	if err != nil {
		return nil, err
	}
	if in.ObserveDomain != nil && !in.Continuous {
		return nil, fmt.Errorf("domain observer requires continuous indexing")
	}
	if in.ResumeUnavailable && (o.Ordering != schema.Isolated || !in.Continuous) {
		return nil, fmt.Errorf("offline recovery requires continuous Isolated indexing")
	}
	in.resumeUnavailable = in.ResumeUnavailable
	if o.Ordering == schema.Omnichain || o.Ordering == schema.Isolated {
		if !in.SourceCheckpoints {
			return nil, fmt.Errorf("%s requires source checkpoints", o.Ordering)
		}
		if in.Continuous && o.Ordering == schema.Omnichain {
			return nil, fmt.Errorf("omnichain requires the bounded finalized runtime format")
		}
		for _, chain := range in.Chains {
			head, ok := in.FinalizedHeads[chain.ID]
			if !ok && in.ResumeUnavailable {
				continue
			}
			cp, err := checkpoint.Decode(head.Checkpoint)
			if err != nil || cp.ChainID != chain.ID || (!in.Continuous && cp.BlockNumber < in.EndBlocks[chain.ID]) {
				return nil, fmt.Errorf("%s runtime end exceeds acquired finality", o.Ordering)
			}
		}
		in.runtimeOmnichain = o.Ordering == schema.Omnichain
		in.runtimeIsolated = o.Ordering == schema.Isolated
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
	opts.liveContinuous = in.Continuous || (!opts.runtimeOmnichain && !opts.runtimeIsolated && (opts.runtimeSetup || opts.runtimeCachePolicy != nil || opts.runtimeInitial))
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
	needsRPCScan := false
	for id, filters := range opts.runtimeLogFilters {
		for _, f := range filters {
			if f["type"] == "trace" && opts.runtimeLogCallbacks[id] == nil {
				return nil, fmt.Errorf("sparse contract calls require their function ABIs")
			}
			if f["type"] != "log" {
				opts.runtimeBlockScan = true
			}
		}
		needsRPCScan = needsRPCScan || !runtimeRPCCacheEnabled(opts, id)
	}
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
	d := &ManagedLiveDomains{
		storage:            s,
		spec:               liveDomainSpec(o, opts),
		openLogScanRPC:     in.OpenLogScanRPC,
		maxQueryParameters: budget,
	}
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
