package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// NewIndexer constructs the worker passed to LaunchApplication. Isolated chains
// acquire blocks independently and commit to chain-keyed rows in one store.
func NewIndexer(cfg config.Config, db *store.Store, handlers map[string]Handler) (ApplicationIndexer, error) {
	if db == nil {
		return nil, fmt.Errorf("indexer requires a store")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(cfg.Chains) == 1 {
		engine, err := newChainWorker(cfg, db, handlers)
		if err != nil {
			return nil, err
		}
		return engine, nil
	}
	if cfg.Ordering != schema.Isolated || db.Ordering != schema.Isolated {
		return nil, fmt.Errorf("multiple native chain workers require isolated ordering; for a coordinated build use historical.RuntimeRunner")
	}
	if cfg.SourcePlan != nil {
		return nil, fmt.Errorf("a compiled source plan belongs to one chain")
	}
	perChain := make(map[string]*config.Config, len(cfg.Chains))
	for _, chain := range cfg.Chains {
		perChain[chain.Name] = &config.Config{Ordering: cfg.Ordering, Chains: []config.Chain{chain}}
	}
	for _, source := range cfg.Blocks {
		c := perChain[source.Chain]
		c.Blocks = append(c.Blocks, source)
	}
	for _, source := range cfg.Accounts {
		c := perChain[source.Chain]
		c.Accounts = append(c.Accounts, source)
	}
	for _, source := range cfg.Contracts {
		c := perChain[source.Chain]
		c.Contracts = append(c.Contracts, source)
	}
	var workers isolatedIndexer
	for _, chain := range cfg.Chains {
		c := perChain[chain.Name]
		if len(c.Blocks)+len(c.Accounts)+len(c.Contracts) == 0 {
			continue
		}
		engine, err := newChainWorker(*c, db, handlers)
		if err != nil {
			return nil, err
		}
		workers = append(workers, engine)
	}
	if len(workers) == 0 {
		return nil, fmt.Errorf("no indexing sources configured")
	}
	return workers, nil
}

func newChainWorker(cfg config.Config, db *store.Store, handlers map[string]Handler) (*Engine, error) {
	engine, err := New(cfg, db, handlers)
	if err != nil {
		return nil, err
	}
	var last uint64
	var ends []*uint64
	for _, s := range cfg.Accounts {
		ends = append(ends, s.EndBlock)
	}
	for _, s := range cfg.Contracts {
		ends = append(ends, s.EndBlock)
	}
	for _, s := range cfg.Blocks {
		ends = append(ends, s.EndBlock)
	}
	for _, end := range ends {
		if end == nil {
			return engine, nil
		}
		last = max(last, *end)
	}
	if len(ends) != 0 {
		engine.endBlock = &last
	}
	return engine, nil
}

type isolatedIndexer []*Engine

func (workers isolatedIndexer) RunIndexing(ctx context.Context, ready func(bool)) error {
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	states := make([]bool, len(workers))
	count := 0
	ready(false)
	for i, engine := range workers {
		wg.Go(func() {
			err := engine.RunIndexing(run, func(value bool) {
				mu.Lock()
				defer mu.Unlock()
				if states[i] == value {
					return
				}
				states[i] = value
				if value {
					count++
				} else {
					count--
				}
				ready(count == len(workers) && run.Err() == nil)
			})
			if err != nil {
				cancel(fmt.Errorf("chain %s: %w", engine.chain.Name, err))
			}
		})
	}
	wg.Wait()
	return context.Cause(run)
}
