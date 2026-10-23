package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// Actions implements the source's granular durable write/commit/recovery path.
// Its owned namespace cannot be opened as an atomic Store (or vice versa).
// Capture triggers must be installed before RawBatch; dropping them retains a
// permanent guard that refuses business DML outside controlled undo.
type Actions struct{ s *Store }

func NewActions(ctx context.Context, backend Backend, model schema.Schema, namespace string, ordering schema.Ordering) (*Actions, error) {
	s, e := newStore(ctx, backend, model, namespace, ordering, true)
	if e != nil {
		return nil, e
	}
	return &Actions{s}, nil
}
func (a *Actions) transaction(ctx context.Context, fn func(Transaction) error) error {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	tx, e := a.s.Backend.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = a.s.lock(ctx, tx); e != nil {
		return e
	}
	if e = prepareLiveQueryChanges(ctx, tx, a.s.Backend.Dialect()); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *Actions) CreateTriggers(ctx context.Context) error {
	return a.transaction(ctx, func(tx Transaction) error { return a.s.createJournalTriggers(ctx, tx) })
}
func (a *Actions) DropTriggers(ctx context.Context) error {
	return a.transaction(ctx, func(tx Transaction) error {
		for _, table := range a.s.Schema.Tables {
			for _, name := range []string{"_ponder_go_capture", "_ponder_go_no_truncate"} {
				if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+schema.Quote(name)+" ON "+a.s.table(table.Name)); e != nil {
					return e
				}
			}
		}
		return nil
	})
}
func (a *Actions) CreateViews(ctx context.Context) error {
	return a.transaction(ctx, func(tx Transaction) error { return a.s.createViews(ctx, tx) })
}
func (a *Actions) RawBatch(ctx context.Context, chain uint64, handler func(*Writer) error) error {
	return a.transaction(ctx, func(tx Transaction) error {
		if e := a.s.eventContext(ctx, tx, checkpoint.MaxString, chain, false); e != nil {
			return e
		}
		w := &Writer{s: a.s, tx: tx, event: Event{Checkpoint: checkpoint.Checkpoint{ChainID: chain}}, encoded: checkpoint.MaxString, changed: map[string]bool{}}
		defer w.closed.Store(true)
		e := handler(w)
		w.closed.Store(true)
		if e != nil {
			return e
		}
		if orm := w.indexingSession(); orm != nil {
			if e = orm.Cache.flush(ctx, false); e != nil {
				return e
			}
		}
		if w.advanced {
			if e := a.s.requireCommitted(ctx, tx, chain); e != nil {
				return e
			}
		}
		return w.err
	})
}
func (s *Store) commitBlock(ctx context.Context, tx Transaction, table string, target checkpoint.Checkpoint) error {
	if _, e := s.model(table); e != nil {
		return e
	}
	encoded, e := checkpoint.Encode(target)
	if e != nil {
		return e
	}
	if encoded == checkpoint.MaxString {
		return fmt.Errorf("MAX checkpoint is reserved for pending writes")
	}
	query := "UPDATE " + s.internal("_ponder_go_journal") + " SET checkpoint=$1,chain_id=$2 WHERE table_name=$3 AND checkpoint=$4"
	args := []any{encoded, target.ChainID, table, checkpoint.MaxString}
	if s.Ordering == schema.Isolated {
		query += " AND chain_id=$2"
	}
	return tx.Exec(ctx, query, args...)
}
func (a *Actions) CommitBlock(ctx context.Context, table string, target checkpoint.Checkpoint) error {
	return a.transaction(ctx, func(tx Transaction) error { return a.s.commitBlock(ctx, tx, table, target) })
}

// CommitBlock inside a RawBatch shares the business transaction; the Actions
// method instead opens the separate durable phase used by the source tests.
func (w *Writer) CommitBlock(ctx context.Context, table string, target checkpoint.Checkpoint) error {
	if w.closed.Load() {
		return &NonRetryableUserError{Message: "indexing function returned"}
	}
	if !w.s.staged {
		return fmt.Errorf("CommitBlock is only valid in staged actions")
	}
	if orm := w.indexingSession(); orm != nil {
		if e := orm.Cache.Flush(ctx); e != nil {
			return e
		}
	}
	return w.s.commitBlock(ctx, w.tx, table, target)
}
func (s *Store) advanceLatest(ctx context.Context, tx Transaction, target checkpoint.Checkpoint) error {
	encoded, e := checkpoint.Encode(target)
	if e != nil {
		return e
	}
	if e = s.requireCommitted(ctx, tx, target.ChainID); e != nil {
		return e
	}
	return tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_progress")+" SET checkpoint=$1 WHERE chain_id=$2", encoded, target.ChainID)
}
func (a *Actions) AdvanceLatest(ctx context.Context, target checkpoint.Checkpoint) error {
	return a.transaction(ctx, func(tx Transaction) error { return a.s.advanceLatest(ctx, tx, target) })
}
func (w *Writer) AdvanceLatest(ctx context.Context, target checkpoint.Checkpoint) error {
	if w.closed.Load() {
		return &NonRetryableUserError{Message: "indexing function returned"}
	}
	if !w.s.staged {
		return fmt.Errorf("AdvanceLatest is only valid in staged actions")
	}
	if orm := w.indexingSession(); orm != nil {
		if e := orm.Cache.Flush(ctx); e != nil {
			return e
		}
	}
	if e := w.s.advanceLatest(ctx, w.tx, target); e != nil {
		return e
	}
	w.advanced = true
	return nil
}
func (a *Actions) Journal(ctx context.Context, table string) ([]Row, error) {
	model, e := a.s.model(table)
	if e != nil {
		return nil, e
	}
	result, e := a.s.Backend.Query(ctx, "SELECT row_data::text AS data,operation::text AS operation,operation_id::text AS operation_id,checkpoint FROM "+a.s.internal("_ponder_go_journal")+" WHERE table_name=$1 ORDER BY operation_id", table)
	if e != nil {
		return nil, e
	}
	rows := make([]Row, 0, len(result.Rows))
	for _, raw := range result.Rows {
		var row Row
		decoder := json.NewDecoder(strings.NewReader(fmt.Sprint(raw["data"])))
		decoder.UseNumber()
		if e = decoder.Decode(&row); e != nil {
			return nil, e
		}
		for _, column := range model.Columns {
			if row[column.Name] != nil {
				switch column.Type {
				case "bigint", "int8", "numeric":
					row[column.Name] = fmt.Sprint(row[column.Name])
				case "integer", "smallint":
					value, e := strconv.ParseInt(fmt.Sprint(row[column.Name]), 10, 64)
					if e != nil {
						return nil, e
					}
					row[column.Name] = value
				}
			}
		}
		for _, name := range []string{"operation", "operation_id"} {
			value, e := strconv.ParseInt(fmt.Sprint(raw[name]), 10, 64)
			if e != nil {
				return nil, e
			}
			row[name] = value
		}
		row["checkpoint"] = fmt.Sprint(raw["checkpoint"])
		rows = append(rows, row)
	}
	return rows, nil
}

type CheckpointState struct {
	ChainName           string `json:"chainName"`
	ChainID             uint64 `json:"chainId"`
	SafeCheckpoint      string `json:"safeCheckpoint"`
	FinalizedCheckpoint string `json:"finalizedCheckpoint"`
	LatestCheckpoint    string `json:"latestCheckpoint"`
}

func (a *Actions) InitializeCheckpoints(ctx context.Context, states []CheckpointState) error {
	for _, state := range states {
		if state.ChainName == "" {
			return fmt.Errorf("chain name is required")
		}
		for _, encoded := range []string{state.SafeCheckpoint, state.FinalizedCheckpoint, state.LatestCheckpoint} {
			cp, e := checkpoint.Decode(encoded)
			if e != nil {
				return e
			}
			if cp.ChainID != state.ChainID {
				return fmt.Errorf("checkpoint chain does not match state")
			}
		}
	}
	return a.transaction(ctx, func(tx Transaction) error {
		for _, state := range states {
			if e := tx.Exec(ctx, "INSERT INTO "+a.s.internal("_ponder_go_chains")+" VALUES ($1,$2)", state.ChainID, state.ChainName); e != nil {
				return e
			}
			if e := tx.Exec(ctx, "INSERT INTO "+a.s.internal("_ponder_go_progress")+" VALUES ($1,$2)", state.ChainID, state.LatestCheckpoint); e != nil {
				return e
			}
			if e := tx.Exec(ctx, "INSERT INTO "+a.s.internal("_ponder_go_finalized")+" VALUES ($1,$2,$3)", state.ChainID, state.FinalizedCheckpoint, state.SafeCheckpoint); e != nil {
				return e
			}
		}
		return nil
	})
}
func (a *Actions) Checkpoints(ctx context.Context) ([]CheckpointState, error) {
	result, e := a.s.Backend.Query(ctx, "SELECT c.chain_name,c.chain_id::text AS chain_id,p.checkpoint AS latest,f.checkpoint AS finalized,f.safe_checkpoint AS safe FROM "+a.s.internal("_ponder_go_chains")+" c JOIN "+a.s.internal("_ponder_go_progress")+" p USING(chain_id) JOIN "+a.s.internal("_ponder_go_finalized")+" f USING(chain_id) ORDER BY c.chain_id")
	if e != nil {
		return nil, e
	}
	states := make([]CheckpointState, 0, len(result.Rows))
	for _, row := range result.Rows {
		id, e := strconv.ParseUint(fmt.Sprint(row["chain_id"]), 10, 64)
		if e != nil {
			return nil, e
		}
		states = append(states, CheckpointState{ChainName: fmt.Sprint(row["chain_name"]), ChainID: id, SafeCheckpoint: fmt.Sprint(row["safe"]), FinalizedCheckpoint: fmt.Sprint(row["finalized"]), LatestCheckpoint: fmt.Sprint(row["latest"])})
	}
	return states, nil
}
func (s *Store) requireCommitted(ctx context.Context, tx Transaction, chain uint64) error {
	query := "SELECT operation_id FROM " + s.internal("_ponder_go_journal") + " WHERE checkpoint=$1"
	args := []any{checkpoint.MaxString}
	if s.Ordering == schema.Isolated {
		query += " AND chain_id=$2"
		args = append(args, chain)
	}
	rows, e := tx.Query(ctx, query+" LIMIT 1", args...)
	if e != nil {
		return e
	}
	if len(rows.Rows) > 0 {
		return fmt.Errorf("pending journal entries require CommitBlock or CrashRecovery first")
	}
	return nil
}
func (a *Actions) Revert(ctx context.Context, target checkpoint.Checkpoint) error {
	return a.s.Revert(ctx, target)
}
func (a *Actions) Finalize(ctx context.Context, target checkpoint.Checkpoint) error {
	return a.s.Finalize(ctx, target)
}

func (a *Actions) Select(ctx context.Context, name string) ([]Row, error) {
	model, e := a.s.model(name)
	if e != nil {
		return nil, e
	}
	result, e := a.s.Backend.Query(ctx, "SELECT row_to_json(t)::text AS row FROM "+a.s.table(name)+" t")
	if e != nil {
		return nil, e
	}
	rows := make([]Row, 0, len(result.Rows))
	for _, raw := range result.Rows {
		physical, e := decodeSnapshot(model, fmt.Sprint(raw["row"]))
		if e != nil {
			return nil, e
		}
		logical := Row{}
		for _, column := range model.Columns {
			logical[property(column)] = physical[column.Name]
		}
		rows = append(rows, logical)
	}
	return rows, nil
}

// CrashRecovery restores all unfinalized journal operations, including assigned
// checkpoints and MAX sentinels. Like the source primitive, it leaves checkpoint
// metadata for the subsequent runtime migration step to update.
func (a *Actions) CrashRecovery(ctx context.Context) error {
	changed := map[string]bool{}
	e := a.transaction(ctx, func(tx Transaction) error {
		if e := a.s.eventContext(ctx, tx, checkpoint.ZeroString, 0, true); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, "SELECT table_name,row_key::text AS key,before_row::text AS before FROM "+a.s.internal("_ponder_go_journal")+" ORDER BY operation_id DESC")
		if e != nil {
			return e
		}
		changed, e = a.s.undoJournal(ctx, tx, rows)
		if e != nil {
			return e
		}
		return tx.Exec(ctx, "DELETE FROM "+a.s.internal("_ponder_go_journal"))
	})
	if e != nil {
		return e
	}
	tables := []string{}
	for name := range changed {
		tables = append(tables, name)
	}
	a.s.notify(tables)
	return nil
}
func (s *Store) undoJournal(ctx context.Context, tx Transaction, rows Result) (map[string]bool, error) {
	changed := map[string]bool{}
	for _, row := range rows.Rows {
		name := fmt.Sprint(row["table_name"])
		model, e := s.model(name)
		if e != nil {
			return nil, e
		}
		key, e := decodeSnapshot(model, fmt.Sprint(row["key"]))
		if e != nil {
			return nil, e
		}
		where, values, e := keyWhere(model, key, 1)
		if e != nil {
			return nil, e
		}
		if e = tx.Exec(ctx, "DELETE FROM "+s.table(name)+" WHERE "+where, values...); e != nil {
			return nil, e
		}
		if row["before"] != nil {
			before, e := decodeUndoRow(model, fmt.Sprint(row["before"]))
			if e != nil {
				return nil, e
			}
			if e = s.writeRows(ctx, tx, model, []Row{before}, Insert); e != nil {
				return nil, e
			}
		}
		changed[name] = true
	}
	return changed, nil
}
