package store

import (
	"context"
	"fmt"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/schema"
)

// FinalizeOmnichainThrough seals the indexed event prefix proven final on all
// participating chains. Each chain retains its own last checkpoint in the prefix.
func (s *Store) FinalizeOmnichainThrough(ctx context.Context, watermark checkpoint.Checkpoint) error {
	if s.staged || s.Ordering != schema.Omnichain {
		return fmt.Errorf("global finalization requires an atomic omnichain store")
	}
	boundary, err := checkpoint.Encode(watermark)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.Backend.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = s.lock(ctx, tx); err != nil {
		return err
	}
	if err = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_finalized")+" AS f (chain_id,checkpoint,safe_checkpoint) SELECT chain_id,MAX(checkpoint),MAX(checkpoint) FROM "+s.internal("_ponder_go_events")+" WHERE checkpoint<=$1 GROUP BY chain_id ON CONFLICT(chain_id) DO UPDATE SET checkpoint=GREATEST(f.checkpoint,excluded.checkpoint),safe_checkpoint=GREATEST(f.safe_checkpoint,excluded.safe_checkpoint)", boundary); err != nil {
		return err
	}
	if err = tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE checkpoint<=$1", boundary); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
