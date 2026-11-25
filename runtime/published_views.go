package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// PublishViewsOnReady wraps a Go indexer so its first successful catch-up
// publishes a PostgreSQL query namespace before the application reports ready.
func PublishViewsOnReady(indexer ApplicationIndexer, db *store.Store, namespace string) (ApplicationIndexer, error) {
	if indexer == nil || db == nil || db.Backend == nil || db.Backend.Dialect() != "postgres" {
		return nil, fmt.Errorf("publishing query views requires an indexer and a PostgreSQL store")
	}
	if err := schema.ValidatePublishedNamespace(db.Namespace, namespace); err != nil {
		return nil, err
	}
	return &publishingIndexer{indexer: indexer, db: db, namespace: namespace}, nil
}

type publishingIndexer struct {
	indexer   ApplicationIndexer
	db        *store.Store
	namespace string
}

func (p *publishingIndexer) RunIndexing(ctx context.Context, ready func(bool)) error {
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var published bool
	var publishErr error
	report := func(caughtUp bool) {
		mu.Lock()
		defer mu.Unlock()
		if run.Err() != nil {
			ready(false)
			return
		}
		if caughtUp && !published {
			publishErr = store.PublishViews(run, p.db.Backend, p.db.Namespace, p.namespace)
			if publishErr != nil {
				ready(false)
				cancel()
				return
			}
			published = true
		}
		ready(caughtUp)
	}
	err := p.indexer.RunIndexing(run, report)
	if err == nil && run.Err() == nil {
		// A bounded indexer may report completion by returning successfully.
		report(true)
	}
	mu.Lock()
	defer mu.Unlock()
	if publishErr != nil {
		return fmt.Errorf("publish query views in %s: %w", p.namespace, publishErr)
	}
	return err
}
