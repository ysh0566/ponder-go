package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type orderedIndexer struct {
	workers []*Engine
	mu      sync.Mutex
	sealed  string
}

type orderedChain struct {
	engine *Engine
	latest Block
	head   uint64
	start  uint64
	gaps   []interval.Interval
	next   Block
	key    string
}

func (r *orderedIndexer) RunIndexing(ctx context.Context, ready func(bool)) error {
	delay := r.workers[0].pollingInterval()
	var endpoints []string
	for _, w := range r.workers {
		delay = min(delay, w.pollingInterval())
		endpoints = append(endpoints, w.chain.WS)
	}
	return pollIndexer(ctx, delay, ready, r.sync, endpoints...)
}

func (e *Engine) blockCheckpoint(b Block) (checkpoint.Checkpoint, error) {
	n, err := Number(b.Number)
	if err != nil {
		return checkpoint.Zero, err
	}
	ts, err := Number(b.Timestamp)
	if err != nil {
		return checkpoint.Zero, err
	}
	cp := checkpoint.Block(ts, e.chain.ID, n, true)
	_, err = checkpoint.Encode(cp)
	return cp, err
}

func (c *orderedChain) advance(ctx context.Context) error {
	if len(c.gaps) == 0 {
		c.key = ""
		return nil
	}
	gap := &c.gaps[0]
	n := uint64(gap[0])
	b, err := c.engine.block(ctx, Hex(n))
	if err != nil {
		return err
	}
	cp, err := c.engine.blockCheckpoint(b)
	if err != nil {
		return err
	}
	if cp.BlockNumber != n || b.Hash == "" {
		return fmt.Errorf("RPC returned wrong block for chain %s height %d", c.engine.chain.Name, n)
	}
	if n != 0 {
		previous, err := c.engine.Store.SyncedBlock(ctx, c.engine.chain.ID, n-1)
		if err != nil {
			return err
		}
		if previous == nil && n > c.start {
			return fmt.Errorf("missing indexed parent of block %d on chain %s", n, c.engine.chain.Name)
		}
		if previous != nil && previous.Hash != b.ParentHash {
			return errIndexingFork
		}
	}
	c.next = b
	c.key = checkpoint.MustEncode(cp)
	if gap[0] == gap[1] {
		c.gaps = c.gaps[1:]
	} else {
		gap[0]++
	}
	return nil
}

func (r *orderedIndexer) sync(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.workers[0].Store.Ordering == schema.Multichain {
		return r.syncMultichain(ctx)
	}
	chains := make([]orderedChain, len(r.workers))
	watermark, finalWatermark := checkpoint.MaxString, checkpoint.MaxString
	allFinished := true
	// Acquire and reconcile every fork before reading any completion intervals.
	// Undo on one chain can make a suffix on a different chain eligible for replay.
	for i, e := range r.workers {
		latest, err := e.acquireHead(ctx)
		if err != nil {
			return false, err
		}
		head, err := e.blockCheckpoint(latest)
		if err != nil {
			return false, err
		}
		if err = e.checkReorg(ctx, head.BlockNumber); err != nil {
			return false, err
		}
		final, err := e.FinalizedBlock(ctx, latest)
		if err != nil {
			return false, err
		}
		safe, err := e.blockCheckpoint(final)
		if err != nil {
			return false, err
		}
		chains[i] = orderedChain{engine: e, latest: latest, head: head.BlockNumber}
		if e.endBlock == nil || safe.BlockNumber < *e.endBlock {
			allFinished = false
			watermark = min(watermark, checkpoint.MustEncode(head))
			finalWatermark = min(finalWatermark, checkpoint.MustEncode(safe))
		}
	}
	if err := processAvailable(ctx, chains, watermark); err != nil {
		return false, err
	}
	if r.sealed != finalWatermark {
		safe, err := checkpoint.Decode(finalWatermark)
		if err != nil {
			return false, err
		}
		var anchors []store.SyncBlock
		for _, c := range chains {
			anchor, err := c.finalAnchor(ctx, safe)
			if err != nil {
				return false, err
			}
			if anchor != nil {
				anchors = append(anchors, *anchor)
			}
		}
		if err := r.workers[0].Store.FinalizeOmnichainThrough(ctx, safe, anchors); err != nil {
			if errors.Is(err, store.ErrIndexedBlockChanged) {
				return false, errIndexingFork
			}
			return false, err
		}
		r.sealed = finalWatermark
	}
	return allFinished, nil
}

// syncMultichain reconciles all available heads before collecting missing ranges.
// A peer rewind can invalidate gaps on any chain. Only omnichain waits for
// the slowest head's checkpoint before applying a block.
func (r *orderedIndexer) syncMultichain(ctx context.Context) (bool, error) {
	var chains []orderedChain
	var unavailable error
	for _, e := range r.workers {
		latest, err := e.acquireHead(ctx)
		var head checkpoint.Checkpoint
		if err == nil {
			head, err = e.blockCheckpoint(latest)
		}
		if err == nil {
			err = e.checkReorg(ctx, head.BlockNumber)
		}
		if err != nil {
			if rpc.IsTransient(err) {
				unavailable = err
				continue
			}
			return false, err
		}
		chains = append(chains, orderedChain{engine: e, latest: latest, head: head.BlockNumber})
	}
	db := r.workers[0].Store
	replay, err := db.PendingMultichainReplay(ctx)
	if err != nil {
		return false, err
	}
	if replay != "" && unavailable != nil {
		// Survivors are part of the replacement batch. An offline peer must
		// not silently move its callbacks behind the new fork on restart.
		return false, unavailable
	}
	if err := processAvailable(ctx, chains, checkpoint.MaxString); err != nil {
		return false, err
	}
	if replay != "" {
		if err := db.FinishMultichainReplay(ctx, replay); err != nil {
			if errors.Is(err, store.ErrIndexedBlockChanged) {
				return false, errIndexingFork
			}
			return false, err
		}
	}
	done := unavailable == nil
	for _, c := range chains {
		through := c.head
		if c.engine.endBlock != nil {
			through = min(through, *c.engine.endBlock)
		}
		finished, err := c.engine.finalizeIndexed(ctx, c.latest, through)
		if err != nil {
			return false, err
		}
		done = done && finished
	}
	return done, unavailable
}

func processAvailable(ctx context.Context, chains []orderedChain, watermark string) error {
	for i := range chains {
		c := &chains[i]
		start, err := c.engine.firstSourceBlock()
		if err != nil {
			return err
		}
		through := c.head
		if c.engine.endBlock != nil {
			through = min(through, *c.engine.endBlock)
		}
		c.start = start
		c.gaps, err = c.engine.missingBlocks(ctx, start, through)
		if err != nil {
			return err
		}
		if err = c.advance(ctx); err != nil {
			return err
		}
	}
	for {
		var picked *orderedChain
		for i := range chains {
			c := &chains[i]
			if c.key != "" && c.key <= watermark && (picked == nil || c.key < picked.key) {
				picked = c
			}
		}
		if picked == nil {
			break
		}
		if err := picked.engine.process(ctx, picked.next); err != nil {
			return err
		}
		if err := picked.advance(ctx); err != nil {
			return err
		}
	}
	return nil
}

// finalAnchor selects a completed block within the global prefix. Chain ID
// breaks ties between blocks with identical timestamps.
func (c *orderedChain) finalAnchor(ctx context.Context, safe checkpoint.Checkpoint) (*store.SyncBlock, error) {
	e := c.engine
	through := c.head
	if e.endBlock != nil {
		through = min(through, *e.endBlock)
	}
	cutoff := safe.BlockTimestamp
	if e.chain.ID == safe.ChainID {
		through = min(through, safe.BlockNumber)
	}
	if e.chain.ID > safe.ChainID && cutoff > 0 {
		cutoff--
	}
	anchor, err := e.Store.IndexedFinalityCandidate(ctx, e.chain.ID, through, cutoff)
	if err != nil {
		return nil, err
	}
	if anchor != nil {
		cp := checkpoint.Block(anchor.Timestamp, anchor.ChainID, anchor.Number, true)
		if checkpoint.Greater(cp, safe) {
			anchor = nil
		}
	}
	if anchor != nil && anchor.Number != c.head {
		block, err := e.block(ctx, Hex(anchor.Number))
		if err != nil {
			return nil, err
		}
		if block.Hash != anchor.Hash {
			return nil, errIndexingFork
		}
	}
	// Confirm the head used to decide the watermark even on an empty chain.
	tip, err := e.block(ctx, Hex(c.head))
	if err != nil {
		return nil, err
	}
	if tip.Hash != c.latest.Hash || (anchor != nil && anchor.Number == c.head && anchor.Hash != tip.Hash) {
		return nil, errIndexingFork
	}
	return anchor, nil
}
