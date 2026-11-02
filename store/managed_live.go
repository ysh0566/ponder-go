package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"ponder.local/ponder-go/checkpoint"
)

const managedLiveVersion = "managed-live-v1"

var (
	ErrManagedLiveClosed          = errors.New("managed live owner is closed")
	ErrManagedLiveLegacyMutation  = errors.New("managed live requires receipt-backed writes; legacy mutation is rejected")
	ErrManagedLiveReceiptConflict = errors.New("managed live open receipt has different inputs")
	ErrManagedLiveOpenIDUsed      = errors.New("managed live open ID is already durable; use a new explicit open attempt")
	ErrManagedLivePendingActions  = errors.New("managed live recovery or activation is not available for this state")
)

// ManagedLiveHead is a freshly acquired finalized block, not a cached public
// checkpoint. Callers own actual RPC acquisition. The checkpoint is block/end.
type ManagedLiveHead struct {
	Checkpoint string `json:"checkpoint"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
}

// ManagedLiveOptions uses a fresh namespace and final endpoints distinct from
// the historical cut. Fresh heads are excluded from immutable plan identity,
// but bound to an exact, caller-named initialization/recovery receipt.
type ManagedLiveOptions struct {
	ManagedFiniteOptions
	OpenID             string
	FinalizedHeads     map[uint64]ManagedLiveHead
	recovery           bool
	coordinated        bool
	liveRuntime        bool
	liveContinuous     bool
	runtimeStarts      map[uint64]uint64
	runtimeCachePolicy map[uint64]bool
	runtimeSetup       bool
	runtimeInitial     bool
	runtimeFactories   bool
}

// ManagedLiveControl binds numeric ordinals, durable finality and safe headers.
// Nonempty history requires the separate v3 recovery constructors.
type ManagedLiveControl struct {
	Version             string                     `json:"version"`
	Generation          uint64                     `json:"generation"`
	Phase               string                     `json:"phase"`
	Next                uint64                     `json:"next"`
	Tail                uint64                     `json:"tail"`
	SafeOrdinal         uint64                     `json:"safeOrdinal"`
	SafeAnchors         map[uint64]string          `json:"safeAnchors"`
	FinalizedHeads      map[uint64]ManagedLiveHead `json:"finalizedHeads"`
	HistoryCuts         map[uint64]uint64          `json:"historyCuts"`
	LastOperation       string                     `json:"lastOperation"`
	CatalogHash         string                     `json:"catalogHash"`
	Batches             uint64                     `json:"batches,omitempty"`
	Reorgs              uint64                     `json:"reorgs,omitempty"`
	Finalizations       uint64                     `json:"finalizations,omitempty"`
	Finality            map[uint64]ManagedLiveHead `json:"finality,omitempty"`
	SafeHeads           map[uint64]ManagedLiveHead `json:"safeHeads,omitempty"`
	SafeJournalBoundary int64                      `json:"safeJournalBoundary,omitempty"`
	Cursors             map[uint64]ManagedLiveHead `json:"cursors,omitempty"`
	Recoveries          uint64                     `json:"recoveries,omitempty"`
	ReplayTargets       map[uint64]string          `json:"replayTargets,omitempty"`
	Serving             bool                       `json:"serving,omitempty"`
	Activations         uint64                     `json:"activations,omitempty"`
	Bootstraps          uint64                     `json:"bootstraps,omitempty"`
	LogicalCursors      map[uint64]string          `json:"logicalCursors,omitempty"`
	LogicalSafe         map[uint64]string          `json:"logicalSafe,omitempty"`
	LogicalFinality     map[uint64]string          `json:"logicalFinality,omitempty"`
}

// ManagedLive reuses the finite owner's actual locks, lease lane and retained
// Close operation. It is a separate on-disk format, never a finite migration.
// Batch writes use independent exact receipts. Every legacy mutation and
// activation route remains sealed; this handle is not a complete runtime.
type ManagedLive struct {
	owner      *ManagedFinite
	opts       ManagedLiveOptions
	recovery   *managedLiveRecoveryEvidence
	previous   *ManagedLiveControl
	beforeHash string
	opened     ManagedLiveControl
	uncertain  atomic.Bool
}

func normalizeManagedLiveOptions(o IndependentStorageOptions, in ManagedLiveOptions) (ManagedLiveOptions, error) {
	base, err := normalizeManagedOptions(o, in.ManagedFiniteOptions)
	if err != nil {
		return ManagedLiveOptions{}, err
	}
	if in.OpenID == "" || len(in.OpenID) > 128 || strings.TrimSpace(in.OpenID) != in.OpenID || strings.ContainsAny(in.OpenID, "\x00\r\n") {
		return ManagedLiveOptions{}, fmt.Errorf("managed live requires an explicit bounded open ID")
	}
	if len(in.FinalizedHeads) != len(base.Chains) {
		return ManagedLiveOptions{}, fmt.Errorf("managed live requires exactly the configured fresh finalized heads")
	}
	out := ManagedLiveOptions{ManagedFiniteOptions: base, OpenID: in.OpenID, FinalizedHeads: map[uint64]ManagedLiveHead{}}
	for _, chain := range base.Chains {
		head, ok := in.FinalizedHeads[chain.ID]
		cp, e := checkpoint.Decode(head.Checkpoint)
		if !ok || e != nil || chain.ID > 9007199254740991 || cp.ChainID != chain.ID || cp.BlockNumber > 9007199254740991 || cp == checkpoint.Max || cp != checkpoint.Block(cp.BlockTimestamp, chain.ID, cp.BlockNumber, true) {
			return ManagedLiveOptions{}, fmt.Errorf("invalid fresh finalized checkpoint for chain %d", chain.ID)
		}
		for _, value := range []string{head.Hash, head.ParentHash} {
			if len(value) != 66 || !strings.HasPrefix(value, "0x") {
				return ManagedLiveOptions{}, fmt.Errorf("invalid fresh finalized block hash")
			}
			if _, e := hex.DecodeString(value[2:]); e != nil {
				return ManagedLiveOptions{}, fmt.Errorf("invalid fresh finalized block hash: %w", e)
			}
		}
		head.Hash, head.ParentHash = strings.ToLower(head.Hash), strings.ToLower(head.ParentHash)
		out.FinalizedHeads[chain.ID] = head
	}
	return out, nil
}

func OpenManagedLiveIndependentPostgres(ctx context.Context, o IndependentStorageOptions, in ManagedLiveOptions) (*IndependentStorage, error) {
	opts, err := normalizeManagedLiveOptions(o, in)
	if err != nil {
		return nil, err
	}
	base := managedStorageOptions{ManagedFiniteOptions: opts.ManagedFiniteOptions, live: &opts}
	return openIndependentPostgres(ctx, o, &base)
}

func OpenManagedLiveIndependentPGlite(ctx context.Context, o IndependentStorageOptions, in ManagedLiveOptions) (*IndependentStorage, error) {
	if o.BusinessDirectory == "" || o.SyncDirectory == "" || o.BusinessDirectory == "memory://" || o.SyncDirectory == "memory://" {
		return nil, fmt.Errorf("managed live requires two persistent filesystem directories")
	}
	opts, err := normalizeManagedLiveOptions(o, in)
	if err != nil {
		return nil, err
	}
	base := managedStorageOptions{ManagedFiniteOptions: opts.ManagedFiniteOptions, live: &opts}
	return openIndependentPGlite(ctx, o, &base)
}

func (m *ManagedLive) historyCuts() map[uint64]uint64 {
	out := map[uint64]uint64{}
	for id, head := range m.opts.FinalizedHeads {
		cp, _ := checkpoint.Decode(head.Checkpoint)
		out[id] = min(cp.BlockNumber, m.opts.EndBlocks[id])
	}
	return out
}

func (m *ManagedLive) requestHash() string {
	return managedHash(struct {
		Identity, OpenID string
		Heads            map[uint64]ManagedLiveHead
		Cuts             map[uint64]uint64
	}{m.owner.identity, m.opts.OpenID, m.opts.FinalizedHeads, m.historyCuts()})
}

func (m *ManagedLive) rejectLegacy(ctx context.Context) error {
	if m.owner.closed.Load() {
		return ErrManagedLiveClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.owner.lost.Load() {
		return ErrLeaseLost
	}
	return ErrManagedLiveLegacyMutation
}

func (m *ManagedLive) createFormat(ctx context.Context, tx Transaction) error {
	s := m.owner.s
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_live_control") + " (key text PRIMARY KEY CHECK(key='control'),payload jsonb NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_live_segments") + " (ordinal bigint PRIMARY KEY CHECK(ordinal>0),payload jsonb NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_live_operations") + " (id text PRIMARY KEY,generation bigint NOT NULL CHECK(generation>0),owner_token text NOT NULL,kind text NOT NULL,request_hash text NOT NULL,previous_operation text NOT NULL,before_hash text NOT NULL,after_hash text NOT NULL,payload jsonb NOT NULL)",
		"CREATE TABLE IF NOT EXISTS " + s.internal("_ponder_go_live_proofs") + " (chain_id numeric NOT NULL,block_number numeric NOT NULL,block_hash text NOT NULL,payload jsonb NOT NULL,PRIMARY KEY(chain_id,block_number,block_hash))",
	} {
		if err := tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (m *ManagedLive) readControl(ctx context.Context, q Querier) (ManagedLiveControl, error) {
	r, err := q.Query(ctx, "SELECT payload::text AS payload FROM "+m.owner.s.internal("_ponder_go_live_control")+" WHERE key='control'")
	if err != nil {
		return ManagedLiveControl{}, err
	}
	if len(r.Rows) != 1 {
		return ManagedLiveControl{}, fmt.Errorf("managed live control is missing")
	}
	var c ManagedLiveControl
	decoder := json.NewDecoder(strings.NewReader(fmt.Sprint(r.Rows[0]["payload"])))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil {
		return c, err
	}
	if err := validateLiveControl(c); err != nil {
		return c, err
	}
	return c, nil
}

// Public metadata may advance only in later receipt-backed transactions. Fresh
// head validation uses the upstream timestamp rule, not latest-as-private-cursor.
func validateLiveFinalizedHeads(opts ManagedLiveOptions, states []CheckpointState) error {
	if len(states) != 0 && len(states) != len(opts.Chains) {
		return fmt.Errorf("live checkpoint chain set is incomplete")
	}
	seen := map[uint64]bool{}
	names := map[uint64]string{}
	for _, chain := range opts.Chains {
		names[chain.ID] = chain.Name
	}
	for _, state := range states {
		head, ok := opts.FinalizedHeads[state.ChainID]
		cp, e := checkpoint.Decode(state.FinalizedCheckpoint)
		fresh, fe := checkpoint.Decode(head.Checkpoint)
		latest, le := checkpoint.Decode(state.LatestCheckpoint)
		safe, se := checkpoint.Decode(state.SafeCheckpoint)
		if !ok || seen[state.ChainID] || state.ChainName != names[state.ChainID] || e != nil || fe != nil || le != nil || se != nil || cp.ChainID != state.ChainID || fresh.ChainID != state.ChainID || latest.ChainID != state.ChainID || safe.ChainID != state.ChainID || state.LatestCheckpoint == checkpoint.MaxString || state.SafeCheckpoint > state.FinalizedCheckpoint || state.FinalizedCheckpoint > state.LatestCheckpoint {
			return fmt.Errorf("invalid live finalized chain identity")
		}
		seen[state.ChainID] = true
		if fresh.BlockTimestamp < cp.BlockTimestamp {
			return fmt.Errorf("finalized block for chain %d cannot move backwards", state.ChainID)
		}
	}
	return nil
}

// Metadata never grants readiness before the later activation stage.
func (m *ManagedLive) Metadata(ctx context.Context) (AppMetadata, error) {
	if _, err := m.Control(ctx); err != nil {
		return AppMetadata{}, err
	}
	return m.owner.Metadata(ctx)
}

func (m *ManagedLive) Ready(ctx context.Context) (bool, error) {
	if m.opts.liveRuntime {
		return m.runtimeReady(ctx)
	}
	a, err := m.Metadata(ctx)
	if err != nil {
		return false, err
	}
	if a.Version != m.version() || a.IsReady != 0 {
		return false, ErrManagedLivePendingActions
	}
	return false, nil
}

func (m *ManagedLive) prepareReopen(ctx context.Context, tx Transaction) error {
	c, err := m.readControl(ctx, tx)
	if err != nil {
		return err
	}
	if c.Version != m.version() {
		return fmt.Errorf("incompatible managed live format; use a new namespace")
	}
	if m.opts.recovery && c.Batches != 0 {
		return m.prepareRecovery(ctx, tx)
	}
	// Batch history requires actual safe recovery, not a bootstrap reopen or a
	// receipt-count shortcut. Preserve all state until that stage is implemented.
	if c.Tail != 0 || c.Batches != 0 {
		return ErrManagedLivePendingActions
	}
	actual, err := m.catalogHash(ctx, tx)
	if err != nil {
		return err
	}
	if actual != c.CatalogHash {
		return fmt.Errorf("managed live complete catalog differs from the durable receipt")
	}
	s := m.owner.s
	for _, name := range []string{"_ponder_go_live_segments", "_ponder_go_live_proofs", "_ponder_go_journal"} {
		r, e := tx.Query(ctx, "SELECT 1 FROM "+s.internal(name)+" LIMIT 1")
		if e != nil {
			return e
		}
		if len(r.Rows) != 0 {
			return ErrManagedLivePendingActions
		}
	}
	states, err := checkpointStates(ctx, tx, s)
	if err != nil {
		return err
	}
	if err = validateLiveFinalizedHeads(m.opts, states); err != nil {
		return err
	}
	duplicate, err := tx.Query(ctx, "SELECT request_hash FROM "+s.internal("_ponder_go_live_operations")+" WHERE id=$1", m.opts.OpenID)
	if err != nil {
		return err
	}
	if len(duplicate.Rows) != 0 {
		if len(duplicate.Rows) != 1 || duplicate.Rows[0]["request_hash"] != m.requestHash() {
			return ErrManagedLiveReceiptConflict
		}
		return ErrManagedLiveOpenIDUsed
	}
	before, err := m.snapshotHash(ctx, tx, c.LastOperation)
	if err != nil {
		return err
	}
	receipts, err := tx.Query(ctx, "SELECT r.id,r.generation::text AS generation,r.after_hash FROM "+s.internal("_ponder_go_live_operations")+" r ORDER BY r.generation")
	if err != nil {
		return err
	}
	if len(receipts.Rows) != int(c.Generation) || receipts.Rows[len(receipts.Rows)-1]["id"] != c.LastOperation || receipts.Rows[len(receipts.Rows)-1]["after_hash"] != before {
		return fmt.Errorf("managed live durable state does not match its exact open receipt")
	}
	m.previous, m.beforeHash = &c, before
	return nil
}

func (m *ManagedLive) recordOpen(ctx context.Context, tx Transaction) error {
	if m.recovery != nil {
		return m.recordRecovery(ctx, tx)
	}
	generation, previous := uint64(1), ""
	if m.previous != nil {
		generation, previous = m.previous.Generation+1, m.previous.LastOperation
	}
	catalog, err := m.catalogHash(ctx, tx)
	if err != nil {
		return err
	}
	c := ManagedLiveControl{Version: m.version(), Generation: generation, Phase: "history", Next: 1, SafeAnchors: map[uint64]string{}, FinalizedHeads: m.opts.FinalizedHeads, HistoryCuts: m.historyCuts(), LastOperation: m.opts.OpenID, CatalogHash: catalog}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	s := m.owner.s
	if err = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_control")+" VALUES('control',$1::jsonb) ON CONFLICT(key) DO UPDATE SET payload=excluded.payload", string(raw)); err != nil {
		return err
	}
	if err = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_operations")+" (id,generation,owner_token,kind,request_hash,previous_operation,before_hash,after_hash,payload) VALUES($1,$2,$3,'open',$4,$5,$6,'',$7::jsonb)", m.opts.OpenID, generation, s.leaseToken, m.requestHash(), previous, m.beforeHash, string(raw)); err != nil {
		return err
	}
	after, err := m.snapshotHash(ctx, tx, m.opts.OpenID)
	if err != nil {
		return err
	}
	if err = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_operations")+" SET after_hash=$1 WHERE id=$2", after, m.opts.OpenID); err != nil {
		return err
	}
	m.opened = c
	return nil
}

func (m *ManagedLive) reconcileOpen(ctx context.Context, commitErr error) error {
	unknown := func(err error) error {
		return &ManagedStageError{Stage: "live-open", Outcome: "unknown", Err: errors.Join(commitErr, err)}
	}
	if ctx.Err() != nil {
		return unknown(ctx.Err())
	}
	tx, err := m.owner.s.Backend.Begin(ctx)
	if err != nil {
		return unknown(err)
	}
	defer tx.Rollback(context.Background())
	if err = m.owner.requireLease(ctx, tx); err != nil {
		return unknown(err)
	}
	r, err := tx.Query(ctx, "SELECT owner_token,request_hash,after_hash FROM "+m.owner.s.internal("_ponder_go_live_operations")+" WHERE id=$1", m.opts.OpenID)
	if err != nil {
		return unknown(err)
	}
	expectedRequest := m.requestHash()
	if m.recovery != nil {
		expectedRequest = m.recoveryHash(*m.recovery, m.owner.s.leaseToken)
	}
	if len(r.Rows) != 1 || r.Rows[0]["owner_token"] != m.owner.s.leaseToken || r.Rows[0]["request_hash"] != expectedRequest {
		return unknown(fmt.Errorf("exact live open receipt not confirmed"))
	}
	actual, err := m.snapshotHash(ctx, tx, m.opts.OpenID)
	if err != nil || r.Rows[0]["after_hash"] != actual {
		return unknown(errors.Join(err, fmt.Errorf("live open receipt state readback differs")))
	}
	return nil
}

// Control reads the exact durable generation. Returned containers do not alias
// the options or in-memory owner. It does not assert readiness or live execution.
func (m *ManagedLive) Control(ctx context.Context) (ManagedLiveControl, error) {
	return m.readVerifiedControl(ctx)
}
