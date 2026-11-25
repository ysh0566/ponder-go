package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ysh0566/ponder-go/schema"
)

// A shared session lock keeps every store opened on this backend out of prune.
type deploymentSession struct {
	pid     uint32
	started time.Time
}

func (p *pgBackend) pinDeployment(ctx context.Context, namespace string) (*deploymentSession, error) {
	p.notifyMu.Lock()
	defer p.notifyMu.Unlock()
	if p.closed {
		return nil, sql.ErrConnDone
	}
	if p.pinConnection == nil {
		conn, err := pgx.Connect(ctx, p.url)
		if err != nil {
			return nil, err
		}
		session := &deploymentSession{pid: conn.PgConn().PID()}
		if err = conn.QueryRow(ctx, "SELECT backend_start FROM pg_stat_get_activity(pg_backend_pid())").Scan(&session.started); err != nil {
			_ = conn.Close(context.Background())
			return nil, err
		}
		p.pinConnection, p.pinSession = conn, session
		p.pinned = make(map[string]bool)
	}
	if p.pinned[namespace] {
		if err := p.pinConnection.Ping(ctx); err != nil {
			return nil, errors.Join(errDeploymentDisconnected, err)
		}
		return p.pinSession, nil
	}
	if _, err := p.pinConnection.Exec(ctx, "SELECT pg_advisory_lock_shared(hashtextextended($1,1))", namespace); err != nil {
		return nil, err
	}
	p.pinned[namespace] = true
	return p.pinSession, nil
}

var errUnregisteredDeployment = errors.New("no current native deployment manifest")
var errDeploymentDisconnected = errors.New("deployment session disconnected; reopen the PostgreSQL backend")

type deployment struct {
	Model       json.RawMessage `json:"model"`
	Ordering    schema.Ordering `json:"ordering"`
	Staged      bool            `json:"staged"`
	LeaseMillis int64           `json:"leaseMillis,omitempty"`
}

func (s *Store) recordDeployment(ctx context.Context, tx Transaction) error {
	if _, ok := s.Backend.(*pgBackend); !ok {
		return nil
	}
	model, err := json.Marshal(s.Schema)
	if err != nil {
		return err
	}
	row, err := json.Marshal(deployment{model, s.Ordering, s.staged, s.leaseTimeout})
	if err != nil {
		return err
	}
	return tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_meta")+" VALUES ('deployment',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(row))
}

func readDeployment(ctx context.Context, backend Backend, tx Transaction, ns string) (*Store, error) {
	if !schema.Identifier(ns) || len(ns) > 45 || !strings.HasPrefix(ns, "ponder_go_") {
		return nil, fmt.Errorf("invalid native deployment schema %q", ns)
	}
	s := &Store{Backend: backend, Namespace: ns, SyncNamespace: ns + "_sync"}
	rows, err := tx.Query(ctx, "SELECT key,value FROM "+s.internal("_ponder_go_meta")+" WHERE key IN ('deployment','fingerprint')")
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, row := range rows.Rows {
		values[fmt.Sprint(row["key"])] = fmt.Sprint(row["value"])
	}
	if values["deployment"] == "" {
		return nil, errUnregisteredDeployment
	}
	var d deployment
	if err = json.Unmarshal([]byte(values["deployment"]), &d); err != nil {
		return nil, fmt.Errorf("schema %q has no current deployment manifest", ns)
	}
	want := fmt.Sprintf("%s:%x:%s:staged=%t", storageVersion, sha256.Sum256(d.Model), d.Ordering, d.Staged)
	if values["fingerprint"] != want {
		return nil, fmt.Errorf("deployment fingerprint differs for %s", ns)
	}
	if err = json.Unmarshal(d.Model, &s.Schema); err != nil {
		return nil, err
	}
	if err = s.Schema.Validate(d.Ordering); err != nil {
		return nil, err
	}
	s.Ordering, s.staged, s.leaseTimeout = d.Ordering, d.Staged, d.LeaseMillis
	owner, err := tx.Query(ctx, "SELECT value FROM "+s.syncTable("_ponder_go_meta")+" WHERE key='owner'")
	if err != nil {
		return nil, err
	}
	if len(owner.Rows) != 1 || owner.Rows[0]["value"] != ns {
		return nil, fmt.Errorf("sync owner differs for %s", ns)
	}
	return s, nil
}

// OpenExisting opens the current atomic deployment without creating or changing
// database objects. Closing its backend releases the shared deployment pin.
func OpenExisting(ctx context.Context, backend Backend, model schema.Schema, namespace string, ordering schema.Ordering) (*Store, error) {
	s, err := describeStore(backend, model, namespace, ordering, false)
	if err != nil {
		return nil, err
	}
	tx, err := backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if err = tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
		return nil, err
	}
	pg, postgres := backend.(*pgBackend)
	if postgres {
		if err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))", s.Namespace); err != nil {
			return nil, err
		}
	}
	fingerprint, err := s.storageFingerprint()
	if err != nil {
		return nil, err
	}
	if err = s.verifyStorageMarkers(ctx, tx, fingerprint); err != nil {
		return nil, err
	}
	if err = s.checkUnmanaged(ctx, tx); err != nil {
		return nil, err
	}
	if postgres {
		if s.deploymentSession, err = pg.pinDeployment(ctx, s.Namespace); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// PublishViews creates a published schema alongside a current native deployment.
func PublishViews(ctx context.Context, backend Backend, namespace, views string) error {
	if err := schema.ValidatePublishedNamespace(namespace, views); err != nil {
		return err
	}
	tx, err := backend.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,2))", views); err != nil {
		return err
	}
	owner, err := tx.Query(ctx, "SELECT obj_description(oid,'pg_namespace') AS owner FROM pg_namespace WHERE nspname=$1", views)
	if err != nil {
		return err
	}
	previous := ""
	names := []string{namespace}
	if len(owner.Rows) > 0 {
		comment, _ := owner.Rows[0]["owner"].(string)
		var ok bool
		previous, ok = strings.CutPrefix(comment, "ponder-go:views:v1:")
		if !ok || !schema.Identifier(previous) {
			return fmt.Errorf("refusing to adopt unowned views schema %q", views)
		}
		if previous != namespace {
			names = append(names, previous)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", name); err != nil {
			return err
		}
	}
	s, err := readDeployment(ctx, backend, tx, namespace)
	if err != nil {
		return err
	}
	s.viewsSchema = views
	affected := s.publishedNames()
	if previous != "" {
		old := s
		if previous != namespace {
			old, err = readDeployment(ctx, backend, tx, previous)
			if err != nil {
				return err
			}
			old.viewsSchema = views
			maps.Copy(affected, old.publishedNames())
		}
		if err = s.preparePublication(ctx, tx, old); err != nil {
			return err
		}
	}
	if err = s.createAliasViews(ctx, tx); err != nil {
		return err
	}
	if previous != namespace {
		// Live readers of the stable name must see existing rows in the new
		// deployment even if neither writer commits again after the switch.
		names := slices.Sorted(maps.Keys(affected))
		if err = tx.Exec(ctx, "SELECT pg_notify($1,json_build_array(name)::text) FROM unnest($2::text[]) AS changed(name)", views+"_live_query", names); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) publishedNames() map[string]bool {
	names := map[string]bool{"_ponder_meta": true, "_ponder_checkpoint": true}
	for _, t := range s.Schema.Tables {
		names[t.Name] = true
	}
	for _, v := range s.Schema.Views {
		names[v.Name] = true
	}
	return names
}

// Check the previous manifest and rewrite dependencies before replacing a view.
func (s *Store) preparePublication(ctx context.Context, tx Transaction, old *Store) error {
	next, prior := s.publishedNames(), old.publishedNames()
	relations, err := tx.Query(ctx, `SELECT c.relname AS name,c.relkind='v' AND EXISTS(
		SELECT FROM pg_rewrite r JOIN pg_depend d ON d.classid='pg_rewrite'::regclass AND d.objid=r.oid AND d.refclassid='pg_class'::regclass
		JOIN pg_class src ON src.oid=d.refobjid JOIN pg_namespace origin ON origin.oid=src.relnamespace
		WHERE r.ev_class=c.oid AND origin.nspname=$2) AS owned
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`, s.viewsSchema, old.Namespace)
	if err != nil {
		return err
	}
	var removed []string
	for _, row := range relations.Rows {
		name := fmt.Sprint(row["name"])
		if !next[name] && !prior[name] {
			continue
		}
		if !prior[name] || row["owned"] != true {
			return fmt.Errorf("unowned relation in published schema: %s", name)
		}
		if !next[name] {
			removed = append(removed, s.aliasTable(name))
		}
	}
	if len(removed) > 0 {
		if err = tx.Exec(ctx, "DROP VIEW "+strings.Join(removed, ",")+" RESTRICT"); err != nil {
			return err
		}
	}
	if old.Namespace == s.Namespace {
		return nil
	}
	triggers := []string{"_ponder_go_progress", "_ponder_go_finalized"}
	for _, t := range old.Schema.Tables {
		triggers = append(triggers, t.Name)
	}
	for _, t := range triggers {
		if err = tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+schema.Quote(s.viewsSchema+"_live_query_notify")+" ON "+old.table(t)); err != nil {
			return err
		}
	}
	return tx.Exec(ctx, "COMMENT ON SCHEMA "+schema.Quote(s.viewsSchema)+" IS '"+s.aliasOwner()+"'")
}
