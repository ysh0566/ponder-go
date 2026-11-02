package store

import (
	"context"
	"ponder.local/ponder-go/schema"
)

// The immutable envelope covers every namespace/ownership record consulted by
// subsequent native Store/SyncMigrator initialization. Cache ROWS remain mutable;
// root and mirror ownership rows, comments and catalog shape do not.
func liveDomainSyncEnvelope(ctx context.Context, q Querier, root string) (string, error) {
	parts := []any{}
	for _, namespace := range []string{root, root + "_sync", root + "_rpc_1"} {
		catalog, err := liveDomainCacheCatalog(ctx, q, namespace)
		if err != nil {
			return "", err
		}
		parts = append(parts, catalog)
	}
	for _, namespace := range []string{root, root + "_sync"} {
		r, err := q.Query(ctx, "SELECT key,value FROM "+schema.Quote(namespace)+"._ponder_go_meta ORDER BY key")
		if err != nil {
			return "", err
		}
		parts = append(parts, r.Rows)
	}
	r, err := q.Query(ctx, "SELECT nspname,obj_description(oid,'pg_namespace') AS ownership FROM pg_namespace WHERE nspname IN($1,$2,$3,$4) ORDER BY nspname", root, root+"_sync", root+"_rpc_1", root+"_legacy")
	if err != nil {
		return "", err
	}
	parts = append(parts, r.Rows)
	return managedHash(parts), nil
}
