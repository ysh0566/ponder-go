package store

import (
	"context"
	"fmt"
	"strconv"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

// HistoricalChainState reads the public checkpoints and the mandatory private
// cursor together. Cursor is never synthesized from latest: a partial setup is
// not a resumable historical session.
type HistoricalChainState struct {
	CheckpointState
	Cursor string `json:"cursor"`
}

type HistoricalSnapshot struct {
	Initialized bool                   `json:"initialized"`
	Chains      []HistoricalChainState `json:"chains"`
}

// HistoricalSnapshot is a fenced, serialized business-domain read. One joined
// statement observes every metadata family (including orphaned/partial rows),
// and the same transaction rejects an unfinished journal. This API does not
// infer, repair, or rewind recovery state in either storage domain.
func (a *Actions) HistoricalSnapshot(ctx context.Context) (HistoricalSnapshot, error) {
	var result HistoricalSnapshot
	if a.s.Ordering != schema.Multichain {
		return result, fmt.Errorf("historical snapshot requires default multichain ordering")
	}
	err := a.transaction(ctx, func(tx Transaction) error {
		rows, err := tx.Query(ctx, "SELECT chain_id::text AS chain_id,c.chain_name,p.checkpoint AS latest,f.checkpoint AS finalized,f.safe_checkpoint AS safe,r.checkpoint AS cursor FROM "+a.s.internal("_ponder_go_chains")+" c FULL OUTER JOIN "+a.s.internal("_ponder_go_progress")+" p USING(chain_id) FULL OUTER JOIN "+a.s.internal("_ponder_go_finalized")+" f USING(chain_id) FULL OUTER JOIN "+a.s.internal("_ponder_go_runtime_cursor")+" r USING(chain_id) ORDER BY chain_id")
		if err != nil {
			return err
		}
		result.Chains = make([]HistoricalChainState, 0, len(rows.Rows))
		for _, row := range rows.Rows {
			id, err := strconv.ParseUint(fmt.Sprint(row["chain_id"]), 10, 64)
			if err != nil || row["chain_name"] == nil || fmt.Sprint(row["chain_name"]) == "" {
				return fmt.Errorf("partial historical chain configuration")
			}
			for _, key := range []string{"latest", "finalized", "safe", "cursor"} {
				encoded, ok := row[key].(string)
				cp, decodeErr := checkpoint.Decode(encoded)
				if !ok || decodeErr != nil || cp.ChainID != id || encoded == checkpoint.MaxString {
					return fmt.Errorf("invalid or missing historical %s checkpoint", key)
				}
			}
			state := HistoricalChainState{CheckpointState: CheckpointState{ChainID: id, ChainName: fmt.Sprint(row["chain_name"]), LatestCheckpoint: row["latest"].(string), FinalizedCheckpoint: row["finalized"].(string), SafeCheckpoint: row["safe"].(string)}, Cursor: row["cursor"].(string)}
			if state.SafeCheckpoint != state.Cursor || state.Cursor != state.LatestCheckpoint || state.FinalizedCheckpoint != state.Cursor {
				return fmt.Errorf("inconsistent historical checkpoint metadata")
			}
			result.Chains = append(result.Chains, state)
		}
		query := "SELECT 1 FROM " + a.s.internal("_ponder_go_journal")
		var args []any
		if len(result.Chains) > 0 {
			// Known-chain journal suffixes can legitimately survive global-prefix
			// pruning. Reject only unsealed writes or rows with no durable chain;
			// never infer or repair their attribution from the current build.
			query += " WHERE checkpoint=$1 OR chain_id NOT IN (SELECT chain_id FROM " + a.s.internal("_ponder_go_chains") + ")"
			args = []any{checkpoint.MaxString}
		}
		pending, err := tx.Query(ctx, query+" LIMIT 1", args...)
		if err != nil {
			return err
		}
		if len(pending.Rows) != 0 {
			return fmt.Errorf("historical snapshot has uninitialized or pending journal state")
		}
		result.Initialized = len(result.Chains) != 0
		return nil
	})
	if err != nil {
		return HistoricalSnapshot{}, err
	}
	return result, nil
}
