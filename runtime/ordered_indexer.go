package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/interval"
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
	for i := range chains {
		c := &chains[i]
		start, err := c.engine.firstSourceBlock()
		if err != nil {
			return false, err
		}
		through := c.head
		if c.engine.endBlock != nil {
			through = min(through, *c.engine.endBlock)
		}
		if start <= through {
			c.gaps, err = c.engine.Store.Missing(ctx, c.engine.chain.ID, c.engine.filter, interval.Interval{int64(start), int64(through)})
			if err != nil {
				return false, err
			}
			if err = c.advance(ctx); err != nil {
				return false, err
			}
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
			return false, err
		}
		if err := picked.advance(ctx); err != nil {
			return false, err
		}
	}
	if r.sealed != finalWatermark {
		// Never discard undo records on evidence from a snapshot that forked while
		// RPC acquisition or the handlers were running.
		for _, c := range chains {
			tip, err := c.engine.block(ctx, Hex(c.head))
			if err != nil {
				return false, err
			}
			if tip.Hash != c.latest.Hash {
				return false, errIndexingFork
			}
		}
		safe, err := checkpoint.Decode(finalWatermark)
		if err != nil {
			return false, err
		}
		if err = r.workers[0].Store.FinalizeOmnichainThrough(ctx, safe); err != nil {
			return false, err
		}
		r.sealed = finalWatermark
	}
	return allFinished, nil
}
