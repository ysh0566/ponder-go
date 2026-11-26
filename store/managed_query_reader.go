package store

import (
	"context"
	"fmt"

	"github.com/ysh0566/ponder-go/schema"
)

// ManagedQueryReader serves committed business data from a separately running
// PostgreSQL RuntimeRunner. The caller owns the backend and the query listener.
type ManagedQueryReader struct {
	QueryReader
}

// OpenManagedQueryReader opens an existing business namespace with SELECT
// privileges. It checks the business model and runtime format in one query.
func OpenManagedQueryReader(ctx context.Context, backend Backend, model schema.Schema, namespace string, ordering schema.Ordering) (*ManagedQueryReader, error) {
	if backend == nil || backend.Dialect() != "postgres" || namespace == "" {
		return nil, fmt.Errorf("managed query service requires PostgreSQL and a business namespace")
	}
	s, err := describeStore(backend, cloneManagedSchema(model), namespace, ordering, true)
	if err != nil {
		return nil, err
	}
	fingerprint, err := s.storageFingerprint()
	if err != nil {
		return nil, err
	}
	row, err := backend.Query(ctx, "SELECT m.value AS fingerprint,a.value->>'version' AS version FROM "+s.internal("_ponder_go_meta")+" m CROSS JOIN "+s.internal("_ponder_go_app")+" a WHERE m.key='fingerprint' AND a.key='app'")
	if err != nil {
		return nil, err
	}
	if len(row.Rows) != 1 || row.Rows[0]["fingerprint"] != fingerprint {
		return nil, fmt.Errorf("business namespace %q does not match the configured schema and ordering", namespace)
	}
	version, _ := row.Rows[0]["version"].(string)
	if !isLiveRuntimeVersion(version) {
		return nil, fmt.Errorf("business namespace %q has unsupported runtime version %q", namespace, version)
	}
	return &ManagedQueryReader{QueryReader{store: s, namedProgress: true}}, nil
}

// Ready reads the writer's last committed publication flag, including when
// the writer is offline.
func (r *ManagedQueryReader) Ready(ctx context.Context) (bool, error) {
	return r.store.readAppReady(ctx)
}
