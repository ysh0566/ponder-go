package runtime

import (
	"context"

	"github.com/ysh0566/ponder-go/interval"
	"github.com/ysh0566/ponder-go/metrics"
)

// Each RunIndexing poller owns its samples. Omnichain may leave its current
// tip waiting for another chain's watermark across several polls.
type polledHeads map[uint64]polledHead

type polledHead struct {
	hash   string
	height uint64
	ack    func(bool)
}

func (p polledHeads) receive(ctx context.Context, e *Engine, b Block) {
	if p == nil || b.Hash == "" {
		return
	}
	n, err := Number(b.Number)
	if err != nil {
		return
	}
	last := p[e.chain.ID]
	if last.hash == b.Hash && last.height == n {
		return
	}
	head := polledHead{hash: b.Hash, height: n}
	if e.endBlock == nil || n <= *e.endBlock {
		if mined, err := Number(b.Timestamp); err == nil {
			head.ack = metrics.StartRealtimeBlock(ctx, e.chain.Name, mined)
		}
	}
	// A newer poll supersedes an unprocessed tip. Capped ranges and old
	// branches can still be acquired by number without becoming head samples.
	p[e.chain.ID] = head
}

func (p polledHeads) retainMissing(chain uint64, gaps []interval.Interval) {
	head := p[chain]
	if head.ack != nil && (len(gaps) == 0 || uint64(gaps[len(gaps)-1][1]) != head.height) {
		head.ack = nil
		p[chain] = head
	}
}

func (p polledHeads) committed(chain uint64, b Block) {
	head, ok := p[chain]
	if !ok {
		return
	}
	n, err := Number(b.Number)
	if err != nil || n != head.height {
		return
	}
	// A lookup by height can return a different branch from the latest poll.
	if head.ack != nil {
		head.ack(head.hash == b.Hash)
	}
	p[chain] = polledHead{height: n, hash: b.Hash}
}
