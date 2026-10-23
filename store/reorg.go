package store

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"strings"
)

// Revert uses the first affected operation ID to undo the complete global suffix
// in multichain mode. Isolated mode only undoes writes belonging to that chain.
func (s *Store) Revert(ctx context.Context, target checkpoint.Checkpoint) error {
	return s.revert(ctx, target, false, false)
}

// RevertAndRewind commits indexing undo and sync provenance rewind together.
// The schemas stay separate; this native backend stores both in one PostgreSQL
// database, so a process exit cannot leave completed intervals on an orphan fork.
func (s *Store) RevertAndRewind(ctx context.Context, target checkpoint.Checkpoint) error {
	return s.revert(ctx, target, true, false)
}

func (s *Store) revert(ctx context.Context, target checkpoint.Checkpoint, rewind, runtimeResume bool) error {
	if s.staged && len(s.Schema.Tables) == 0 && !runtimeResume {
		return nil
	}
	encoded, e := checkpoint.Encode(target)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = s.requireLease(ctx, tx); e != nil {
		return e
	}
	if s.staged {
		if e = prepareLiveQueryChanges(ctx, tx, s.Backend.Dialect()); e != nil {
			return e
		}
		if e = s.requireCommitted(ctx, tx, target.ChainID); e != nil {
			return e
		}
		if runtimeResume {
			if e = (&Actions{s}).singleRuntimeChain(ctx, tx, target.ChainID); e != nil {
				return e
			}
			if e = tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); e != nil {
				return e
			}
		}
	}
	if e = s.eventContext(ctx, tx, encoded, target.ChainID, true); e != nil {
		return e
	}
	final, e := tx.Query(ctx, "SELECT safe_checkpoint FROM "+s.internal("_ponder_go_finalized")+" WHERE chain_id=$1", target.ChainID)
	if e != nil {
		return e
	}
	if len(final.Rows) > 0 && encoded < fmt.Sprint(final.Rows[0]["safe_checkpoint"]) {
		return fmt.Errorf("reorg target is before safe checkpoint; shadow rebuild required")
	}
	predicate := "checkpoint>$1"
	args := []any{encoded}
	if s.Ordering != schema.Omnichain {
		predicate += " AND chain_id=$2"
		args = append(args, target.ChainID)
	}
	eventPredicate := predicate
	eventArgs := args
	if s.Ordering == schema.Multichain {
		r, e := tx.Query(ctx, "SELECT MIN(operation_id)::text AS id FROM "+s.internal("_ponder_go_journal")+" WHERE "+predicate, args...)
		if e != nil {
			return e
		}
		if len(r.Rows) > 0 && r.Rows[0]["id"] != nil {
			cut := r.Rows[0]["id"]
			predicate = "operation_id>=$1"
			args = []any{cut}
			eventPredicate = "(chain_id=$1 AND checkpoint>$2) OR operation_end>=$3"
			eventArgs = []any{target.ChainID, encoded, cut}
		}
	}
	rows, e := tx.Query(ctx, "SELECT operation_id::text AS id,table_name,row_key::text AS key,before_row::text AS before FROM "+s.internal("_ponder_go_journal")+" WHERE "+predicate+" ORDER BY operation_id DESC", args...)
	if e != nil {
		return e
	}
	changed, e := s.undoJournal(ctx, tx, rows)
	if e != nil {
		return e
	}
	if e = tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE "+predicate, args...); e != nil {
		return e
	}
	if !s.staged {
		if e = tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_events")+" WHERE "+eventPredicate, eventArgs...); e != nil {
			return e
		}
		if e = tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_progress")); e != nil {
			return e
		}
		if e = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_progress")+" SELECT chain_id,MAX(checkpoint) FROM (SELECT chain_id,checkpoint FROM "+s.internal("_ponder_go_events")+" UNION ALL SELECT chain_id,safe_checkpoint AS checkpoint FROM "+s.internal("_ponder_go_finalized")+") AS progress GROUP BY chain_id"); e != nil {
			return e
		}
	}
	if rewind {
		if e = s.rewindSync(ctx, tx, target.ChainID, target.BlockNumber); e != nil {
			return e
		}
	}
	if runtimeResume {
		if e = s.runtimeCursor(ctx, tx, target); e != nil {
			return e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	tables := []string{}
	for name := range changed {
		tables = append(tables, name)
	}
	if !runtimeResume {
		s.notify(tables)
	}
	return nil
}

// Finalize removes only an undo prefix that no future valid reorg may require.
// Multichain retains finalized writes behind an unfinalized write on another chain.
func (s *Store) Finalize(ctx context.Context, target checkpoint.Checkpoint) error {
	encoded, e := checkpoint.Encode(target)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = s.lock(ctx, tx); e != nil {
		return e
	}
	if e = s.requireLease(ctx, tx); e != nil {
		return e
	}
	if s.staged {
		if e = prepareLiveQueryChanges(ctx, tx, s.Backend.Dialect()); e != nil {
			return e
		}
		if e = s.requireCommitted(ctx, tx, target.ChainID); e != nil {
			return e
		}
	}
	if len(s.Schema.Tables) == 0 {
		if e = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_finalized")+" SET checkpoint=$1,safe_checkpoint=$1 WHERE chain_id=$2", encoded, target.ChainID); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if !s.staged {
		r, e := tx.Query(ctx, "SELECT checkpoint FROM "+s.internal("_ponder_go_progress")+" WHERE chain_id=$1", target.ChainID)
		if e != nil {
			return e
		}
		if len(r.Rows) == 0 || encoded > fmt.Sprint(r.Rows[0]["checkpoint"]) {
			return fmt.Errorf("cannot finalize beyond indexed progress")
		}
	}
	r, e := tx.Query(ctx, "SELECT checkpoint FROM "+s.internal("_ponder_go_finalized")+" WHERE chain_id=$1", target.ChainID)
	if e != nil {
		return e
	}
	if s.staged && len(r.Rows) == 0 {
		journal, e := tx.Query(ctx, "SELECT operation_id FROM "+s.internal("_ponder_go_journal")+" LIMIT 1")
		if e != nil {
			return e
		}
		if len(journal.Rows) == 0 {
			if e = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_finalized")+" SET checkpoint=$1 WHERE chain_id=$2", encoded, target.ChainID); e != nil {
				return e
			}
			return tx.Commit(ctx)
		}
		return fmt.Errorf("chain checkpoint state must be initialized before finalization")
	}
	if len(r.Rows) > 0 && encoded < fmt.Sprint(r.Rows[0]["checkpoint"]) {
		return fmt.Errorf("finality regressed")
	}
	if e = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_finalized")+" VALUES ($1,$2,$3) ON CONFLICT(chain_id) DO UPDATE SET checkpoint=excluded.checkpoint", target.ChainID, encoded, checkpoint.ZeroString); e != nil {
		return e
	}
	predicate := "checkpoint<=$1"
	args := []any{encoded}
	if s.Ordering == schema.Isolated {
		predicate += " AND chain_id=$2"
		args = append(args, target.ChainID)
	}
	if s.Ordering == schema.Multichain {
		r, e := tx.Query(ctx, "SELECT MIN(j.operation_id)::text AS id FROM "+s.internal("_ponder_go_journal")+" j LEFT JOIN "+s.internal("_ponder_go_finalized")+" f ON f.chain_id=j.chain_id WHERE j.checkpoint>COALESCE(f.checkpoint,$1)", checkpoint.ZeroString)
		if e != nil {
			return e
		}
		predicate = "TRUE"
		args = nil
		if r.Rows[0]["id"] != nil {
			predicate = "operation_id<$1"
			args = []any{r.Rows[0]["id"]}
		}
	}
	safe, e := tx.Query(ctx, "SELECT chain_id::text AS chain_id, MAX(checkpoint) AS checkpoint FROM "+s.internal("_ponder_go_journal")+" WHERE "+predicate+" GROUP BY chain_id", args...)
	if e != nil {
		return e
	}
	if e = tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE "+predicate, args...); e != nil {
		return e
	}
	for _, row := range safe.Rows {
		if e = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_finalized")+" SET safe_checkpoint=GREATEST(safe_checkpoint,$1) WHERE chain_id=$2", row["checkpoint"], row["chain_id"]); e != nil {
			return e
		}
	}
	if s.Ordering != schema.Multichain {
		where := "chain_id=$2"
		if s.Ordering == schema.Omnichain {
			where = "TRUE"
		}
		if where == "TRUE" {
			e = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_finalized")+" SET safe_checkpoint=GREATEST(safe_checkpoint,$1)", encoded)
		} else {
			e = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_finalized")+" SET safe_checkpoint=GREATEST(safe_checkpoint,$1) WHERE "+where, encoded, target.ChainID)
		}
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) QueryReadOnly(ctx context.Context, sql string, args ...any) (Result, error) {
	tx, e := s.Backend.Begin(ctx)
	if e != nil {
		return Result{}, e
	}
	defer tx.Rollback(context.Background())
	if e = tx.Exec(ctx, "SET TRANSACTION READ ONLY"); e != nil {
		return Result{}, e
	}
	if e = tx.Exec(ctx, "SET LOCAL search_path TO "+schema.Quote(s.Namespace)+",pg_catalog"); e != nil {
		return Result{}, e
	}
	if e = tx.Exec(ctx, "SET LOCAL statement_timeout='5s'"); e != nil {
		return Result{}, e
	}
	result, e := tx.Query(ctx, strings.TrimSpace(sql), args...)
	return result, e
}
