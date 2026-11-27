package store

import (
	"context"
	"iter"

	"github.com/ysh0566/ponder-go/schema"
)

// QueryReader borrows a store's read operations and change notifications.
// Its lifetime is bounded by the owner of the store.
type QueryReader struct {
	store         *Store
	namedProgress bool
}

func (s *Store) QueryReader() QueryReader { return QueryReader{store: s} }

func (d *ManagedLiveDomains) BusinessReader() QueryReader {
	return d.storage.BusinessStore().QueryReader()
}

// Cancellable reads use a pooled PostgreSQL session. Interrupting a query
// must not discard the session holding the lifetime writer locks.
func readBackend(backend Backend) Backend {
	if pg, ok := backend.(*independentPGBackend); ok {
		return pg.pgBackend
	}
	return backend
}

func (r QueryReader) Ordering() schema.Ordering { return r.store.Ordering }
func (r QueryReader) Available() bool           { return r.store != nil }
func (r QueryReader) Namespace() string         { return r.store.Namespace }
func (r QueryReader) Dialect() string           { return r.store.Backend.Dialect() }

// Progress keys standalone managed checkpoints by their committed chain names.
// Readers borrowed from an indexing process use chain IDs.
func (r QueryReader) Progress(ctx context.Context) (map[string]string, error) {
	return r.store.readProgress(ctx, readBackend(r.store.Backend), r.namedProgress)
}

func (r QueryReader) Relations() iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, t := range r.store.Schema.Tables {
			if !yield(t.Name) {
				return
			}
		}
		for _, v := range r.store.Schema.Views {
			if !yield(v.Name) {
				return
			}
		}
	}
}

func (r QueryReader) QueryDependencies(names []string) ([]string, error) {
	return r.store.QueryDependencies(names)
}
func (r QueryReader) QueryReadOnlyInNamespace(ctx context.Context, namespace, sql string, args ...any) (Result, error) {
	return queryReadOnlyInNamespace(ctx, readBackend(r.store.Backend), namespace, sql, args...)
}
func (r QueryReader) Changes() *QueryChanges { return r.store.QueryChanges() }
func (r QueryReader) Listen(ctx context.Context, changes *QueryChanges, namespace string) (*QueryNotifications, error) {
	return changes.Listen(ctx, readBackend(r.store.Backend), namespace, r.store.Schema.Tables)
}
