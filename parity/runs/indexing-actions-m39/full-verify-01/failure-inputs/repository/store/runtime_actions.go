package store

import (
	"context"
	"fmt"

	"ponder.local/ponder-go/checkpoint"
)

// RuntimeStore exposes this lifecycle's fenced store to the native scheduler.
// Atomic Apply still rejects it; business writes must use the Actions path.
func (d *Lifecycle) RuntimeStore() *Store { return d.s }

func (a *Actions) singleRuntimeChain(ctx context.Context, tx Transaction, chain uint64) error {
	r, e := tx.Query(ctx, "SELECT chain_id::text AS id FROM "+a.s.internal("_ponder_go_chains"))
	if e != nil {
		return e
	}
	if len(r.Rows) != 1 || fmt.Sprint(r.Rows[0]["id"]) != fmt.Sprint(chain) {
		return fmt.Errorf("native staged runtime currently requires exactly one initialized chain")
	}
	return nil
}

// InitializeRuntime commits setup writes and initial checkpoint metadata
// together. Every setup write is captured until this confirmed baseline seals
// its journal prefix. There is no unjournaled business write path.
func (a *Actions) InitializeRuntime(ctx context.Context, state CheckpointState, setup func(*Writer) error) error {
	return a.HistoricalBatch(ctx, state.ChainID, func(w *Writer) error {
		r, e := w.tx.Query(ctx, "SELECT chain_id FROM "+a.s.internal("_ponder_go_chains"))
		if e != nil {
			return e
		}
		if len(r.Rows) > 0 {
			return fmt.Errorf("runtime already initialized")
		}
		if e = setup(w); e != nil {
			return e
		}
		target, e := checkpoint.Decode(state.SafeCheckpoint)
		if e != nil {
			return e
		}
		if target.ChainID != state.ChainID || state.SafeCheckpoint != state.LatestCheckpoint || state.SafeCheckpoint != state.FinalizedCheckpoint || state.ChainName == "" {
			return fmt.Errorf("invalid initial runtime checkpoint state")
		}
		for _, table := range a.s.Schema.Tables {
			if e = w.CommitBlock(ctx, table.Name, target); e != nil {
				return e
			}
		}
		if e = w.tx.Exec(ctx, "INSERT INTO "+a.s.internal("_ponder_go_chains")+" VALUES($1,$2)", state.ChainID, state.ChainName); e != nil {
			return e
		}
		if e = w.tx.Exec(ctx, "INSERT INTO "+a.s.internal("_ponder_go_progress")+" VALUES($1,$2)", state.ChainID, state.LatestCheckpoint); e != nil {
			return e
		}
		if e = w.tx.Exec(ctx, "INSERT INTO "+a.s.internal("_ponder_go_finalized")+" VALUES($1,$2,$3)", state.ChainID, state.FinalizedCheckpoint, state.SafeCheckpoint); e != nil {
			return e
		}
		if e = a.s.runtimeCursor(ctx, w.tx, target); e != nil {
			return e
		}
		return a.s.pruneInitialSafePrefix(ctx, w.tx)
	})
}

// ResumeRuntime follows successful Lifecycle.Migrate recovery: business data is
// at safe while the source lifecycle primitive deliberately retained old latest.
// Rewind provenance/cache and reset the private cursor atomically, without publishing a
// realtime notification before any replacement block has committed.
func (a *Actions) ResumeRuntime(ctx context.Context, safe checkpoint.Checkpoint) error {
	return a.transaction(ctx, func(tx Transaction) error {
		if e := a.singleRuntimeChain(ctx, tx, safe.ChainID); e != nil {
			return e
		}
		if e := tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); e != nil {
			return e
		}
		if e := a.s.rewindSync(ctx, tx, safe.ChainID, safe.BlockNumber); e != nil {
			return e
		}
		return a.s.runtimeCursor(ctx, tx, safe)
	})
}

// ProcessRuntimeBlock mirrors the source realtime transaction: handlers write
// MAX sentinels, all tables commitBlock, then latest (and its NOTIFY) commit in
// that same transaction. Historical blocks additionally seal safe/finalized in
// the transaction. The bool reports whether the callback executed.
func (a *Actions) ProcessRuntimeBlock(ctx context.Context, target checkpoint.Checkpoint, historical bool, handler func(*Writer) error) (bool, error) {
	encoded, e := checkpoint.Encode(target)
	if e != nil {
		return false, e
	}
	if encoded == checkpoint.MaxString {
		return false, fmt.Errorf("MAX is reserved for pending writes")
	}
	applied := false
	changed := []string{}
	e = a.HistoricalBatch(ctx, target.ChainID, func(w *Writer) error {
		if e := a.singleRuntimeChain(ctx, w.tx, target.ChainID); e != nil {
			return e
		}
		r, e := w.tx.Query(ctx, "SELECT COALESCE(c.checkpoint,p.checkpoint) AS checkpoint FROM "+a.s.internal("_ponder_go_progress")+" p LEFT JOIN "+a.s.internal("_ponder_go_runtime_cursor")+" c USING(chain_id) WHERE p.chain_id=$1", target.ChainID)
		if e != nil {
			return e
		}
		if len(r.Rows) != 1 {
			return fmt.Errorf("missing runtime latest checkpoint")
		}
		if encoded <= fmt.Sprint(r.Rows[0]["checkpoint"]) {
			return nil
		}
		if historical {
			if e = w.tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); e != nil {
				return e
			}
		}
		if e = handler(w); e != nil {
			return e
		}
		for _, table := range a.s.Schema.Tables {
			if e = w.CommitBlock(ctx, table.Name, target); e != nil {
				return e
			}
		}
		if e = w.AdvanceLatest(ctx, target); e != nil {
			return e
		}
		if e = a.s.runtimeCursor(ctx, w.tx, target); e != nil {
			return e
		}
		if historical {
			if e = w.tx.Exec(ctx, "UPDATE "+a.s.internal("_ponder_go_finalized")+" SET checkpoint=$1,safe_checkpoint=$1 WHERE chain_id=$2", encoded, target.ChainID); e != nil {
				return e
			}
			if e = a.s.pruneInitialSafePrefix(ctx, w.tx); e != nil {
				return e
			}
		} else {
			r, e = w.tx.Query(ctx, "SELECT DISTINCT table_name FROM "+a.s.internal("_ponder_go_journal")+" WHERE chain_id=$1 AND checkpoint=$2 ORDER BY table_name", target.ChainID, encoded)
			if e != nil {
				return e
			}
			for _, row := range r.Rows {
				changed = append(changed, fmt.Sprint(row["table_name"]))
			}
		}
		applied = true
		return nil
	})
	if e != nil {
		return false, e
	}
	if applied && !historical {
		a.s.notify(changed)
	}
	return applied, nil
}

// RevertRuntime is the single-chain scheduler's rollback. Public Revert keeps
// the original primitive's retained-latest behavior. Here the resume cursor and
// canonical cache are reset with undo, and no realtime notification is emitted
// for the intermediate reorg transaction.
func (a *Actions) RevertRuntime(ctx context.Context, target checkpoint.Checkpoint) error {
	return a.s.revert(ctx, target, true, true)
}

// The source keeps public latest unchanged during reorg/recovery. This private
// durable scheduler cursor permits replacement blocks and failed-retry restart
// without exposing an early metadata update or publishing a notification.
func (s *Store) runtimeCursor(ctx context.Context, tx Transaction, target checkpoint.Checkpoint) error {
	cp, e := checkpoint.Encode(target)
	if e != nil {
		return e
	}
	return tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_runtime_cursor")+" VALUES($1,$2) ON CONFLICT(chain_id) DO UPDATE SET checkpoint=excluded.checkpoint", target.ChainID, cp)
}
func (a *Actions) RuntimeCheckpoint(ctx context.Context, chain uint64) (string, error) {
	r, e := a.s.Backend.Query(ctx, "SELECT COALESCE(c.checkpoint,p.checkpoint) AS checkpoint FROM "+a.s.internal("_ponder_go_progress")+" p LEFT JOIN "+a.s.internal("_ponder_go_runtime_cursor")+" c USING(chain_id) WHERE p.chain_id=$1", chain)
	if e != nil {
		return "", e
	}
	if len(r.Rows) != 1 {
		return "", fmt.Errorf("runtime checkpoint is missing")
	}
	return fmt.Sprint(r.Rows[0]["checkpoint"]), nil
}
