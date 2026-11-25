package store

import (
	"context"
	"fmt"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/schema"
)

// FinalizeOmnichainThrough seals the indexed event prefix proven final on all
// participating chains. Each chain retains its own last checkpoint in the prefix.
func (s *Store) FinalizeOmnichainThrough(ctx context.Context, watermark checkpoint.Checkpoint, blocks []SyncBlock) error {
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
	for _, block := range blocks {
		cp, err := checkpoint.Encode(checkpoint.Block(block.Timestamp, block.ChainID, block.Number, true))
		if err != nil {
			return err
		}
		if cp > boundary {
			return fmt.Errorf("omnichain block is outside the final prefix")
		}
		receipt, err := tx.Query(ctx, "SELECT 1 FROM "+s.syncTable("blocks")+" b JOIN "+s.internal("_ponder_go_events")+" e ON e.chain_id=b.chain_id WHERE b.chain_id=$1 AND b.number=$2 AND b.hash=$3 AND b.timestamp=$4 AND e.event_id=$5 AND e.checkpoint=$6", block.ChainID, block.Number, block.Hash, block.Timestamp, block.CompletionID(), cp)
		if err != nil {
			return err
		}
		if len(receipt.Rows) != 1 {
			return ErrIndexedBlockChanged
		}
		if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_finalized")+" AS f (chain_id,checkpoint,safe_checkpoint) VALUES ($1,$2,$2) ON CONFLICT(chain_id) DO UPDATE SET checkpoint=GREATEST(f.checkpoint,excluded.checkpoint),safe_checkpoint=GREATEST(f.safe_checkpoint,excluded.safe_checkpoint)", block.ChainID, cp); err != nil {
			return err
		}
		if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE chain_id=$1 AND checkpoint<=$2", block.ChainID, cp); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
