package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"ponder.local/ponder-go/checkpoint"
)

func liveUint(row map[string]any, key string) (uint64, error) {
	return strconv.ParseUint(fmt.Sprint(row[key]), 10, 64)
}

func liveInt(row map[string]any, key string) (int64, error) {
	return strconv.ParseInt(fmt.Sprint(row[key]), 10, 64)
}

func (m *ManagedLive) readLiveJournal(ctx context.Context, q Querier, before int64) ([]managedLiveJournalEntry, error) {
	r, err := q.Query(ctx, "SELECT r.operation_id::text AS id,r.table_name,r.row_key::text AS key_json,r.before_row::text AS before_json,r.row_data::text AS row_json,r.operation::text AS operation,r.checkpoint,r.chain_id::text AS chain_id FROM "+m.owner.s.internal("_ponder_go_journal")+" r WHERE r.operation_id>$1 ORDER BY r.operation_id", before)
	if err != nil {
		return nil, err
	}
	rows := make([]managedLiveJournalEntry, 0, len(r.Rows))
	for _, raw := range r.Rows {
		id, e := liveInt(raw, "id")
		chain, ce := liveUint(raw, "chain_id")
		op, oe := liveInt(raw, "operation")
		if e != nil || ce != nil || oe != nil || op < 0 || op > 2 {
			return nil, fmt.Errorf("invalid managed journal number")
		}
		previous := json.RawMessage("null")
		if raw["before_json"] != nil {
			previous = json.RawMessage(fmt.Sprint(raw["before_json"]))
		}
		rows = append(rows, managedLiveJournalEntry{ID: id, ChainID: chain, Operation: int(op), Table: fmt.Sprint(raw["table_name"]), Checkpoint: fmt.Sprint(raw["checkpoint"]), Key: json.RawMessage(fmt.Sprint(raw["key_json"])), Before: previous, Row: json.RawMessage(fmt.Sprint(raw["row_json"]))})
	}
	return rows, nil
}

func (m *ManagedLive) readDurable(ctx context.Context, q Querier) (managedLiveDurable, string, error) {
	return m.readDurableState(ctx, q, false)
}

func (m *ManagedLive) readDurableState(ctx context.Context, q Querier, prior bool) (managedLiveDurable, string, error) {
	var d managedLiveDurable
	c, err := m.readControl(ctx, q)
	if err != nil {
		return d, "", err
	}
	d.Control = c
	catalog, err := m.catalogHash(ctx, q)
	if err != nil || catalog != c.CatalogHash {
		return d, "", errors.Join(err, fmt.Errorf("managed live complete catalog changed"))
	}
	s := m.owner.s
	r, err := q.Query(ctx, "SELECT r.id,r.generation::text AS generation,r.owner_token,r.kind,r.request_hash,r.previous_operation,r.before_hash,r.after_hash,r.payload::text AS payload FROM "+s.internal("_ponder_go_live_operations")+" r ORDER BY r.generation,r.id")
	if err != nil {
		return d, "", err
	}
	for _, row := range r.Rows {
		generation, err := liveUint(row, "generation")
		if err != nil {
			return d, "", err
		}
		d.Operations = append(d.Operations, managedLiveOperation{ID: fmt.Sprint(row["id"]), Generation: generation, OwnerToken: fmt.Sprint(row["owner_token"]), Kind: fmt.Sprint(row["kind"]), RequestHash: fmt.Sprint(row["request_hash"]), PreviousOperation: fmt.Sprint(row["previous_operation"]), BeforeHash: fmt.Sprint(row["before_hash"]), AfterHash: fmt.Sprint(row["after_hash"]), Payload: json.RawMessage(fmt.Sprint(row["payload"]))})
	}
	r, err = q.Query(ctx, "SELECT r.ordinal::text AS ordinal,r.payload::text AS payload FROM "+s.internal("_ponder_go_live_segments")+" r ORDER BY r.ordinal")
	if err != nil {
		return d, "", err
	}
	for _, row := range r.Rows {
		ordinal, e := liveUint(row, "ordinal")
		var segment ManagedLiveSegment
		if e != nil || decodeLiveJSON([]byte(fmt.Sprint(row["payload"])), &segment) != nil || ordinal != segment.Ordinal {
			return d, "", fmt.Errorf("managed segment ordinal/payload differs")
		}
		d.Segments = append(d.Segments, segment)
	}
	r, err = q.Query(ctx, "SELECT r.chain_id::text AS chain_id,r.block_number::text AS block_number,r.block_hash,r.payload::text AS payload FROM "+s.internal("_ponder_go_live_proofs")+" r ORDER BY r.chain_id,r.block_number,r.block_hash")
	if err != nil {
		return d, "", err
	}
	for _, row := range r.Rows {
		chain, ce := liveUint(row, "chain_id")
		number, ne := liveUint(row, "block_number")
		var head ManagedLiveHead
		if decodeLiveJSON([]byte(fmt.Sprint(row["payload"])), &head) != nil {
			return d, "", ErrManagedLiveBatchConflict
		}
		normalized, cp, e := normalizeLiveHead(head)
		if ce != nil || ne != nil || e != nil || normalized != head || chain != cp.ChainID || number != cp.BlockNumber || row["block_hash"] != head.Hash {
			return d, "", fmt.Errorf("managed acquired proof columns/payload differ")
		}
		d.Proofs = append(d.Proofs, head)
	}
	d.Journal, err = m.readLiveJournal(ctx, q, 0)
	if err != nil {
		return d, "", err
	}
	r, err = q.Query(ctx, "SELECT chain_id::text AS id,c.chain_name,p.checkpoint AS latest,f.checkpoint AS finalized,f.safe_checkpoint AS safe,r.checkpoint AS cursor FROM "+s.internal("_ponder_go_chains")+" c FULL OUTER JOIN "+s.internal("_ponder_go_progress")+" p USING(chain_id) FULL OUTER JOIN "+s.internal("_ponder_go_finalized")+" f USING(chain_id) FULL OUTER JOIN "+s.internal("_ponder_go_runtime_cursor")+" r USING(chain_id) ORDER BY chain_id")
	if err != nil {
		return d, "", err
	}
	for _, row := range r.Rows {
		id, err := liveUint(row, "id")
		if err != nil {
			return d, "", err
		}
		d.States = append(d.States, managedLiveCursorState{ChainID: id, Name: fmt.Sprint(row["chain_name"]), Latest: fmt.Sprint(row["latest"]), Finalized: fmt.Sprint(row["finalized"]), Safe: fmt.Sprint(row["safe"]), Cursor: fmt.Sprint(row["cursor"])})
	}
	token, generation, heads := s.leaseToken, m.opened.Generation, m.opts.FinalizedHeads
	if prior {
		app, e := q.Query(ctx, "SELECT lease_token,value->>'version' AS version FROM "+s.internal("_ponder_go_app")+" WHERE key='app'")
		if e != nil {
			return d, "", e
		}
		if len(app.Rows) != 1 || app.Rows[0]["version"] != m.version() {
			return d, "", ErrManagedLiveBatchConflict
		}
		token, generation, heads = fmt.Sprint(app.Rows[0]["lease_token"]), c.Generation, c.FinalizedHeads
	}
	ordered, err := m.validateDurableOwner(d, token, generation, heads)
	if err != nil {
		return d, "", err
	}
	if m.opts.liveRuntime {
		if err := m.validateRuntimeServing(ctx, q, c, token); err != nil {
			return d, "", err
		}
	}
	actual, err := m.snapshotHash(ctx, q, c.LastOperation)
	if err != nil {
		return d, "", err
	}
	if err := confirmLiveSnapshot(ordered, actual); err != nil {
		return d, "", err
	}
	return d, actual, nil
}

func (m *ManagedLive) readVerifiedControl(ctx context.Context) (ManagedLiveControl, error) {
	if err := m.usable(ctx); err != nil {
		return ManagedLiveControl{}, err
	}
	unlock, err := m.owner.lockMetadataRead(ctx)
	if err != nil {
		return ManagedLiveControl{}, err
	}
	defer unlock()
	backend := m.owner.s.Backend
	if m.owner.control != nil {
		backend = m.owner.control
	}
	tx, err := backend.Begin(ctx)
	if err != nil {
		return ManagedLiveControl{}, err
	}
	defer tx.Rollback(context.Background())
	if err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return ManagedLiveControl{}, err
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return ManagedLiveControl{}, err
	}
	d, _, err := m.readDurable(ctx, tx)
	if err == nil {
		err = m.owner.requireLease(ctx, tx)
	}
	if err == nil {
		err = m.usable(ctx)
	}
	return d.Control, err
}

func (m *ManagedLive) insertLiveProof(ctx context.Context, tx Transaction, head ManagedLiveHead) error {
	cp, _ := checkpoint.Decode(head.Checkpoint)
	// A global reorg can rewind another chain while retaining its acquired
	// proof. Replay may reuse that exact proof, never repair differing evidence.
	existing, err := tx.Query(ctx, "SELECT payload::text AS payload FROM "+m.owner.s.internal("_ponder_go_live_proofs")+" WHERE chain_id=$1 AND block_number=$2 AND block_hash=$3", cp.ChainID, cp.BlockNumber, head.Hash)
	if err != nil {
		return err
	}
	if len(existing.Rows) != 0 {
		var retained ManagedLiveHead
		if len(existing.Rows) != 1 || decodeLiveJSON([]byte(fmt.Sprint(existing.Rows[0]["payload"])), &retained) != nil || retained != head {
			return ErrManagedLiveBatchConflict
		}
		return nil
	}
	raw, err := json.Marshal(head)
	if err != nil {
		return err
	}
	return tx.Exec(ctx, "INSERT INTO "+m.owner.s.internal("_ponder_go_live_proofs")+" (chain_id,block_number,block_hash,payload) VALUES($1,$2,$3,$4::jsonb)", cp.ChainID, cp.BlockNumber, head.Hash, string(raw))
}

func (m *ManagedLive) initializeLiveBaselines(ctx context.Context, tx Transaction, baselines map[uint64]ManagedLiveHead, initial map[uint64]string) error {
	s := m.owner.s
	for _, chain := range m.opts.Chains {
		head := baselines[chain.ID]
		position := head.Checkpoint
		if m.opts.runtimeInitial {
			position = initial[chain.ID]
		}
		if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_chains")+" VALUES($1,$2)", chain.ID, chain.Name); err != nil {
			return err
		}
		if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_progress")+" VALUES($1,$2)", chain.ID, position); err != nil {
			return err
		}
		if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_finalized")+" VALUES($1,$2,$2)", chain.ID, position); err != nil {
			return err
		}
		cp, _ := checkpoint.Decode(position)
		if err := s.runtimeCursor(ctx, tx, cp); err != nil {
			return err
		}
		if err := m.insertLiveProof(ctx, tx, head); err != nil {
			return err
		}
	}
	return s.createJournalTriggers(ctx, tx)
}

func (m *ManagedLive) runLiveBlock(ctx context.Context, tx Transaction, segment *ManagedLiveSegment, before int64, targets map[uint64]string, handler func(*Writer, ManagedLiveBlock) error) error {
	s := m.owner.s
	cp, _ := checkpoint.Decode(segment.Block.Head.Checkpoint)
	if err := s.requireCommitted(ctx, tx, cp.ChainID); err != nil {
		return err
	}
	if err := s.eventContext(ctx, tx, checkpoint.MaxString, cp.ChainID, false); err != nil {
		return err
	}
	w := &Writer{s: s, tx: tx, event: Event{ID: fmt.Sprintf("%s/%d", segment.OperationID, segment.Ordinal), Checkpoint: cp}, encoded: checkpoint.MaxString, changed: map[string]bool{}}
	// Per-block caches never publish predictions beyond the enclosing batch.
	defer func() {
		w.closed.Store(true)
		if orm := w.indexingSession(); orm != nil {
			orm.Cache.mu.Lock()
			clearSessionTables(orm.Cache.tables)
			orm.Cache.mu.Unlock()
		}
	}()
	if handler != nil {
		if err := handler(w, segment.Block); err != nil {
			w.closed.Store(true)
			return err
		}
	}
	w.closed.Store(true)
	if orm := w.indexingSession(); orm != nil {
		if err := orm.Cache.flush(ctx, false); err != nil {
			return err
		}
	}
	if w.err != nil {
		return w.err
	}
	for _, table := range s.Schema.Tables {
		if err := s.commitBlock(ctx, tx, table.Name, cp); err != nil {
			return err
		}
	}
	if err := s.requireCommitted(ctx, tx, cp.ChainID); err != nil {
		return err
	}
	rows, err := m.readLiveJournal(ctx, tx, before)
	if err != nil {
		return err
	}
	segment.Journal, err = liveJournalSpan(before, segment.Block.Head, rows)
	if err != nil {
		return err
	}
	if err := m.insertLiveProof(ctx, tx, segment.Block.Head); err != nil {
		return err
	}
	if err := s.runtimeCursor(ctx, tx, cp); err != nil {
		return err
	}
	if liveReplayPublishes(segment.Block.Head, targets) {
		if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_progress")+" SET checkpoint=$1 WHERE chain_id=$2", segment.Block.Head.Checkpoint, cp.ChainID); err != nil {
			return err
		}
	}

	raw, err := json.Marshal(segment)
	if err != nil {
		return err
	}
	return tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_segments")+" (ordinal,payload) VALUES($1,$2::jsonb)", segment.Ordinal, string(raw))
}

// ApplyBatch commits every supplied block, including empty blocks, in one
// business transaction. The callback must join its own goroutines. This is a
// storage primitive for execution and v3 replay, not historical setup or runtime.
func (m *ManagedLive) ApplyBatch(ctx context.Context, in ManagedLiveBatch, acquired *ManagedLiveAcquisition, handler func(*Writer, ManagedLiveBlock) error) (ManagedLiveBatchResult, error) {
	if err := m.usable(ctx); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	batch, err := normalizeLiveBatch(m.opts, in)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if err := m.requireAcquisition(batch, acquired); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	s := m.owner.s
	if !s.mu.TryLock() {
		return ManagedLiveBatchResult{}, ErrManagedLiveBusy
	}
	defer s.mu.Unlock()
	if err := m.usable(ctx); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	tx, err := s.Backend.Begin(ctx)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	defer tx.Rollback(context.Background())
	if err := s.lock(ctx, tx); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	d, beforeHash, err := m.readDurable(ctx, tx)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	for _, op := range d.Operations {
		if op.ID == batch.ID {
			evidence, err := exactLiveRetry(batch, m.batchHash(batch), s.leaseToken, op)
			if err == nil {
				err = m.owner.requireLease(ctx, tx)
			}
			if err != nil {
				return ManagedLiveBatchResult{}, err
			}
			return ManagedLiveBatchResult{OperationID: batch.ID, Segments: append([]ManagedLiveSegment(nil), evidence.Segments...)}, err
		}
	}
	segments, cursors, after, err := planLiveBatch(m.opts, d.Control, batch)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if err := m.validateBatchFinality(d, after, segments); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if m.opts.runtimeSetup && d.Control.Batches == 0 {
		if cb, ok := ctx.Value(runtimeSetupKey{}).(*runtimeSetupCallback); !ok || cb == nil || cb.fn == nil {
			return ManagedLiveBatchResult{}, ErrManagedLiveBatchConflict
		}
	}
	if err := prepareLiveQueryChanges(ctx, tx, s.Backend.Dialect()); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if !m.opts.liveRuntime || !d.Control.Serving {
		if err := tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); err != nil {
			return ManagedLiveBatchResult{}, err
		}
	}
	if d.Control.Batches == 0 {
		if err := m.initializeLiveBaselines(ctx, tx, batch.Baselines, batch.InitialCheckpoints); err != nil {
			return ManagedLiveBatchResult{}, err
		}
		if m.opts.runtimeSetup {
			if err := m.runRuntimeSetup(ctx, tx, batch.Baselines, batch.InitialCheckpoints); err != nil {
				return ManagedLiveBatchResult{}, fmt.Errorf("runtime setup/flush: %w", err)
			}
		}
	}
	boundary := d.Control.SafeJournalBoundary
	if len(d.Journal) > 0 {
		boundary = d.Journal[len(d.Journal)-1].ID
	}
	for i := range segments {
		if err := m.runLiveBlock(ctx, tx, &segments[i], boundary, d.Control.ReplayTargets, handler); err != nil {
			if m.opts.liveRuntime {
				return ManagedLiveBatchResult{}, fmt.Errorf("runtime execute/flush: %w", err)
			}
			return ManagedLiveBatchResult{}, err
		}
		boundary = segments[i].Journal.After
	}
	after.CatalogHash, err = m.catalogHash(ctx, tx)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	controlJSON, err := json.Marshal(after)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_control")+" SET payload=$1::jsonb WHERE key='control'", string(controlJSON)); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	evidence := managedLiveBatchEvidence{Version: managedLiveBatchVersion, Batch: batch, AcquisitionHash: acquired.requestHash, BeforeCursors: cursors, Segments: segments, AfterControl: after}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_operations")+" (id,generation,owner_token,kind,request_hash,previous_operation,before_hash,after_hash,payload) VALUES($1,$2,$3,'batch',$4,$5,$6,'',$7::jsonb)", batch.ID, batch.Generation, s.leaseToken, acquired.requestHash, d.Control.LastOperation, beforeHash, string(payload)); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	afterHash, err := m.snapshotHash(ctx, tx, batch.ID)
	if err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_operations")+" SET after_hash=$1 WHERE id=$2", afterHash, batch.ID); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if _, _, err := m.readDurable(ctx, tx); err != nil {
		if m.opts.liveRuntime {
			return ManagedLiveBatchResult{}, fmt.Errorf("runtime post-batch audit: %w", err)
		}
		return ManagedLiveBatchResult{}, err
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return ManagedLiveBatchResult{}, err
	}
	if m.opts.liveRuntime {
		if original, ok := ctx.Value(runtimeCommitContextKey{}).(context.Context); ok {
			if err := original.Err(); err != nil {
				return ManagedLiveBatchResult{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(context.Background())
		if reconcile := m.reconcileBatch(ctx, batch, acquired, err); reconcile != nil {
			m.uncertain.Store(true)
			return ManagedLiveBatchResult{}, reconcile
		}
	}
	return ManagedLiveBatchResult{Applied: true, OperationID: batch.ID, Segments: append([]ManagedLiveSegment(nil), segments...)}, nil
}

func (m *ManagedLive) reconcileBatch(ctx context.Context, batch ManagedLiveBatch, acquired *ManagedLiveAcquisition, commitErr error) error {
	unknown := func(err error) error {
		return &ManagedStageError{Stage: "live-batch", Outcome: "unknown", Err: errors.Join(commitErr, err)}
	}
	if err := ctx.Err(); err != nil {
		return unknown(err)
	}
	tx, err := m.owner.s.Backend.Begin(ctx)
	if err != nil {
		return unknown(err)
	}
	defer tx.Rollback(context.Background())
	if err := m.owner.s.lock(ctx, tx); err != nil {
		return unknown(err)
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return unknown(err)
	}
	d, _, err := m.readDurable(ctx, tx)
	if err != nil {
		return unknown(err)
	}
	for _, op := range d.Operations {
		if op.ID == batch.ID {
			if _, err := exactLiveRetry(batch, acquired.requestHash, m.owner.s.leaseToken, op); err != nil {
				return unknown(err)
			}
			if err := m.owner.requireLease(ctx, tx); err != nil {
				return unknown(err)
			}
			return nil
		}
	}
	return unknown(fmt.Errorf("exact batch receipt not confirmed"))
}
