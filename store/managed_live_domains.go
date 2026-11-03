package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

const managedLiveDomainsVersion = "managed-live-domains-v4"

var ErrManagedLiveDomainsPending = errors.New("managed live cross-domain operation requires reconciliation")

// ManagedLiveDomains owns the two fault domains. It exposes neither raw backend
// nor a cache handle that could bypass its pending-operation fence. Sync callbacks
// are scoped native transactions; callers must join their work before returning.
// This primitive does not activate a runtime or claim distributed atomicity.
type ManagedLiveDomains struct {
	storage            *IndependentStorage
	mu                 sync.Mutex
	spec               string
	uncertain          bool
	domainObserver     func(context.Context, ManagedLiveDomainStage) error
	runtimeScopeMu     sync.Mutex
	runtimeScope       *ManagedRuntimeScope
	runtimeBatch       atomic.Bool
	servingPublication atomic.Bool
}

// ManagedLiveDomainReorg binds acquired orphan headers, not timestamp order.
// Orphaned must be one contiguous old branch after Ancestor and cover every
// executed block on that chain. Raw event data and intervals are never pruned.
type ManagedLiveDomainReorg struct {
	Reorg    ManagedLiveReorg  `json:"reorg"`
	Orphaned []ManagedLiveHead `json:"orphaned"`
}

type ManagedLiveDomainResult struct {
	OperationID       string                  `json:"operationId"`
	Outcome           string                  `json:"outcome"` // reorg or covered-by-recovery
	BusinessOperation string                  `json:"businessOperation"`
	BusinessHash      string                  `json:"businessHash"`
	Reorg             *ManagedLiveReorgResult `json:"reorg,omitempty"`
}

type liveDomainIntent struct {
	Request                                        ManagedLiveDomainReorg `json:"request"`
	Spec, Identity, Owner, RequestHash, BeforeHash string
	Before                                         ManagedLiveControl
	Deleted                                        []string
}
type liveDomainAck struct {
	IntentHash string
	Result     ManagedLiveDomainResult
}
type liveDomainRecord struct {
	Sequence                          uint64
	Kind, ID, Previous, Payload, Hash string
}
type liveDomainLedger struct {
	Binding *liveDomainBinding
	Records []liveDomainRecord
	Intents map[string]liveDomainIntent
	Acks    map[string]liveDomainAck
	Pending string
	Head    string
}

func liveDomainSpec(o IndependentStorageOptions, opts ManagedLiveOptions) string {
	base := managedHash(struct {
		Version, Business, Sync, Build, Plan, Views string
		Schema                                      schema.Schema
		Ordering                                    schema.Ordering
		Chains                                      []MigrationChain
		Ends                                        map[uint64]uint64
	}{(&ManagedLive{opts: opts}).version(), o.BusinessNamespace, o.SyncNamespace, opts.BuildID, opts.PlanID, opts.ViewsSchema, o.Schema, o.Ordering, opts.Chains, opts.EndBlocks})
	if opts.liveRuntime {
		runtime := managedHash(struct {
			Base   string
			Starts map[uint64]uint64
		}{base, opts.runtimeStarts})
		if opts.runtimeInitial {
			identity := managedHash(struct {
				Runtime  string
				RPCCache map[uint64]bool
				Setup    bool
			}{runtime, opts.runtimeCachePolicy, opts.runtimeSetup})
			if logOnlyOptions(opts) {
				return managedHash(struct {
					Runtime string
					Filters any
				}{identity, opts.runtimeLogFilters})
			}
			return identity
		}
		if opts.runtimeCachePolicy != nil {
			return managedHash(struct {
				Runtime  string
				RPCCache map[uint64]bool
			}{runtime, opts.runtimeCachePolicy})
		}
		return runtime
	}
	return base

}

func liveDomainTable(namespace, name string) string {
	return schema.Quote(namespace) + "." + schema.Quote("_ponder_go_domains_"+name)
}

func openLiveDomains(ctx context.Context, o IndependentStorageOptions, in ManagedLiveOptions, pg bool) (*ManagedLiveDomains, error) {
	opts, err := normalizeManagedLiveOptions(o, in)
	if err != nil {
		return nil, err
	}
	opts.recovery = true
	opts.coordinated = true
	if !pg && (o.BusinessDirectory == "" || o.SyncDirectory == "" || o.BusinessDirectory == "memory://" || o.SyncDirectory == "memory://") {
		return nil, fmt.Errorf("managed domains require persistent directories")
	}
	base := &managedStorageOptions{ManagedFiniteOptions: opts.ManagedFiniteOptions, live: &opts}
	var s *IndependentStorage
	if pg {
		s, err = openIndependentPostgres(ctx, o, base)
	} else {
		s, err = openIndependentPGlite(ctx, o, base)
	}
	if err != nil {
		return nil, err
	}
	d := &ManagedLiveDomains{storage: s, spec: liveDomainSpec(o, opts)}
	if err = d.Resume(ctx); err != nil {
		_ = s.Close(context.Background())
		return nil, err
	}
	return d, nil
}
func OpenManagedLiveDomainsPostgres(ctx context.Context, o IndependentStorageOptions, in ManagedLiveOptions) (*ManagedLiveDomains, error) {
	return openLiveDomains(ctx, o, in, true)
}
func OpenManagedLiveDomainsPGlite(ctx context.Context, o IndependentStorageOptions, in ManagedLiveOptions) (*ManagedLiveDomains, error) {
	return openLiveDomains(ctx, o, in, false)
}
func (d *ManagedLiveDomains) Close(ctx context.Context) error {
	if d.live().opts.liveRuntime {
		if err := d.runtimeQuiescent(); err != nil {
			return err
		}
	}
	if !d.mu.TryLock() {
		return ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	return d.storage.Close(ctx)
}
func (d *ManagedLiveDomains) live() *ManagedLive { return d.storage.live }
func (d *ManagedLiveDomains) table(name string) string {
	return liveDomainTable(d.storage.syncCache.s.Namespace, name)
}

func (d *ManagedLiveDomains) readLedger(ctx context.Context, q Querier) (liveDomainLedger, error) {
	l := liveDomainLedger{Intents: map[string]liveDomainIntent{}, Acks: map[string]liveDomainAck{}, Head: managedHash(d.spec)}
	r, err := q.Query(ctx, "SELECT spec,sequence::text AS sequence,hash,catalog FROM "+d.table("head")+" WHERE key='head'")
	if err != nil {
		return l, err
	}
	if len(r.Rows) != 1 || r.Rows[0]["spec"] != d.spec {
		return l, ErrManagedLiveReceiptConflict
	}
	head := r.Rows[0]
	catalog, e := liveDomainCatalog(ctx, q, d.storage.syncCache.s.Namespace)
	if e != nil || head["catalog"] != catalog {
		return l, errors.Join(ErrManagedLiveReceiptConflict, e)
	}
	n, err := liveUint(head, "sequence")
	if err != nil {
		return l, err
	}
	r, err = q.Query(ctx, "SELECT sequence::text AS sequence,kind,id,previous,payload,hash FROM "+d.table("log")+" ORDER BY sequence")
	if err != nil {
		return l, err
	}
	for _, row := range r.Rows {
		seq, e := liveUint(row, "sequence")
		record := liveDomainRecord{Sequence: seq, Kind: fmt.Sprint(row["kind"]), ID: fmt.Sprint(row["id"]), Previous: fmt.Sprint(row["previous"]), Payload: fmt.Sprint(row["payload"])}
		if e != nil || seq != uint64(len(l.Records)+1) || record.Previous != l.Head || !liveID(record.ID) {
			return l, ErrManagedLiveReceiptConflict
		}
		record.Hash = managedHash(record)
		if row["hash"] != record.Hash {
			return l, ErrManagedLiveReceiptConflict
		}
		switch record.Kind {
		case "bind":
			var binding liveDomainBinding
			if len(l.Records) != 0 || decodeLiveJSON([]byte(record.Payload), &binding) != nil || binding.Operation != record.ID || binding.Identity != d.live().owner.identity || !liveDigest(binding.Hash) || !liveDigest(binding.CacheCatalog) {
				return l, ErrManagedLiveReceiptConflict
			}
			l.Binding = &binding
		case "generation":
			var f managedRuntimeFence
			if !d.live().opts.liveRuntime || l.Binding == nil || l.Pending != "" || decodeLiveJSON([]byte(record.Payload), &f) != nil || f.Operation != record.ID || f.Identity != d.live().owner.identity || !liveDigest(f.BusinessHash) {
				return l, ErrManagedLiveReceiptConflict
			}
		case "prepare":
			var intent liveDomainIntent
			if decodeLiveJSON([]byte(record.Payload), &intent) != nil || l.Binding == nil || intent.Spec != d.spec || intent.Identity != d.live().owner.identity || intent.Request.Reorg.ID != record.ID || l.Pending != "" {
				return l, ErrManagedLiveReceiptConflict
			}
			if _, ok := l.Intents[record.ID]; ok {
				return l, ErrManagedLiveReceiptConflict
			}
			l.Intents[record.ID] = intent
			l.Pending = record.ID
		case "ack":
			var ack liveDomainAck
			intent, ok := l.Intents[record.ID]
			if !ok || l.Pending != record.ID || decodeLiveJSON([]byte(record.Payload), &ack) != nil || ack.IntentHash != managedHash(intent) || ack.Result.OperationID != record.ID {
				return l, ErrManagedLiveReceiptConflict
			}
			l.Acks[record.ID] = ack
			l.Pending = ""
		default:
			return l, ErrManagedLiveReceiptConflict
		}
		l.Records = append(l.Records, record)
		l.Head = record.Hash
	}
	if l.Binding != nil {
		catalog, e := liveDomainSyncEnvelope(ctx, q, d.storage.syncCache.s.Namespace)
		if e != nil || catalog != l.Binding.CacheCatalog {
			return l, errors.Join(ErrManagedLiveReceiptConflict, e)
		}
	}
	if uint64(len(l.Records)) != n || head["hash"] != l.Head {
		return l, ErrManagedLiveReceiptConflict
	}
	return l, nil
}

func (d *ManagedLiveDomains) appendLedger(ctx context.Context, tx Transaction, l liveDomainLedger, kind, id string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	r := liveDomainRecord{Sequence: uint64(len(l.Records) + 1), Kind: kind, ID: id, Previous: l.Head, Payload: string(raw)}
	r.Hash = managedHash(r)
	if err = tx.Exec(ctx, "INSERT INTO "+d.table("log")+" VALUES($1,$2,$3,$4,$5,$6)", r.Sequence, r.Kind, r.ID, r.Previous, r.Payload, r.Hash); err != nil {
		return err
	}
	return tx.Exec(ctx, "UPDATE "+d.table("head")+" SET sequence=$1,hash=$2 WHERE key='head'", r.Sequence, r.Hash)
}

func (d *ManagedLiveDomains) business(ctx context.Context) (managedLiveDurable, string, error) {
	m := d.live()
	s := m.owner.s
	if err := m.usable(ctx); err != nil {
		return managedLiveDurable{}, "", err
	}
	if !s.mu.TryLock() {
		return managedLiveDurable{}, "", ErrManagedLiveBusy
	}
	defer s.mu.Unlock()
	tx, err := s.Backend.Begin(ctx)
	if err != nil {
		return managedLiveDurable{}, "", err
	}
	defer tx.Rollback(context.Background())
	if err = s.lock(ctx, tx); err != nil {
		return managedLiveDurable{}, "", err
	}
	if err = m.owner.requireLease(ctx, tx); err != nil {
		return managedLiveDurable{}, "", err
	}
	return m.readDurable(ctx, tx)
}
func (d *ManagedLiveDomains) gate(ctx context.Context) error {
	if d.uncertain {
		return ErrManagedLiveDomainsPending
	}
	l, _, _, err := d.audit(ctx)
	if err != nil {
		return err
	}
	if l.Pending != "" {
		return ErrManagedLiveDomainsPending
	}
	return nil
}
func (d *ManagedLiveDomains) Control(ctx context.Context) (ManagedLiveControl, error) {
	if !d.mu.TryLock() {
		return ManagedLiveControl{}, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return ManagedLiveControl{}, err
	}
	return d.live().Control(ctx)
}
func (d *ManagedLiveDomains) Ready(ctx context.Context) (bool, error) {
	if d.live().opts.liveRuntime {
		if err := d.live().usable(ctx); err != nil {
			return false, err
		}
		if !d.servingPublication.Load() {
			return false, nil
		}
		if d.runtimeBatch.Load() {
			d.runtimeScopeMu.Lock()
			scope := d.runtimeScope
			d.runtimeScopeMu.Unlock()
			if scope != nil {
				scope.mu.Lock()
				cancelled := !scope.sealed && scope.context.Err() != nil
				scope.mu.Unlock()
				if cancelled {
					return false, nil
				}
			}
			return true, nil
		}
	}

	if !d.mu.TryLock() {
		return false, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return false, err
	}
	return d.live().Ready(ctx)
}
func (d *ManagedLiveDomains) WithSync(ctx context.Context, fn func(*SourceSyncStore) error) error {
	if d.live().opts.liveRuntime {
		return ErrManagedLiveLegacyMutation
	}
	if !d.mu.TryLock() {
		return ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return err
	}
	source, err := d.storage.syncCache.SourceSyncStore(0)
	if err != nil {
		return err
	}
	return source.WithTransaction(ctx, fn)
}
func (d *ManagedLiveDomains) RetainBatch(ctx context.Context, b ManagedLiveBatch) (*ManagedLiveAcquisition, error) {
	if !d.mu.TryLock() {
		return nil, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return nil, err
	}
	return d.live().RetainBatch(ctx, b)
}
func (d *ManagedLiveDomains) ApplyBatch(ctx context.Context, b ManagedLiveBatch, a *ManagedLiveAcquisition, fn func(*Writer, ManagedLiveBlock) error) (ManagedLiveBatchResult, error) {
	if d.live().opts.liveRuntime {
		return ManagedLiveBatchResult{}, ErrManagedLiveLegacyMutation
	}
	if !d.mu.TryLock() {
		return ManagedLiveBatchResult{}, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	return d.live().ApplyBatch(ctx, b, a, fn)
}
func (d *ManagedLiveDomains) RetainFinalize(ctx context.Context, r ManagedLiveFinalize) (*ManagedLiveFinalizeAcquisition, error) {
	if !d.mu.TryLock() {
		return nil, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return nil, err
	}
	return d.live().RetainFinalize(ctx, r)
}
func (d *ManagedLiveDomains) ApplyFinalize(ctx context.Context, r ManagedLiveFinalize, a *ManagedLiveFinalizeAcquisition) (ManagedLiveFinalizeResult, error) {
	if !d.mu.TryLock() {
		return ManagedLiveFinalizeResult{}, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	return d.live().ApplyFinalize(ctx, r, a)
}

func normalizeDomainReorg(opts ManagedLiveOptions, in ManagedLiveDomainReorg) (ManagedLiveDomainReorg, error) {
	r, err := normalizeLiveReorg(opts, in.Reorg)
	if err != nil {
		return in, err
	}
	out := ManagedLiveDomainReorg{Reorg: r, Orphaned: append([]ManagedLiveHead(nil), in.Orphaned...)}
	sort.Slice(out.Orphaned, func(i, j int) bool {
		a, _ := checkpoint.Decode(out.Orphaned[i].Checkpoint)
		b, _ := checkpoint.Decode(out.Orphaned[j].Checkpoint)
		return a.BlockNumber < b.BlockNumber
	})
	previous := r.Ancestor
	_, cp, _ := normalizeLiveHead(previous)
	for i, h := range out.Orphaned {
		head, next, e := normalizeLiveHead(h)
		if e != nil || head != h || next.ChainID != cp.ChainID || next.BlockNumber != cp.BlockNumber+1 || head.ParentHash != previous.Hash || next.BlockTimestamp < cp.BlockTimestamp {
			return out, ErrManagedLiveBatchConflict
		}
		out.Orphaned[i] = head
		previous, cp = head, next
	}
	return out, nil
}
