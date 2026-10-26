package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ponder.local/ponder-go/schema"
)

// IndependentStorageOptions names two constructor-owned storage domains. Names
// must be explicit: deriving either domain from the other can alias its physical
// sync schemas. PGlite directories are persistent paths or empty/"memory://".
type IndependentStorageOptions struct {
	PostgresURL                      string
	BusinessDirectory, SyncDirectory string
	BusinessNamespace, SyncNamespace string
	Schema                           schema.Schema
	Ordering                         schema.Ordering
}

// IndependentStorage owns separate connection pools/processes. It intentionally
// exposes no sync Store or backend, so the sync domain is not a business writer.
// The integration owner must seal and join bindings, clients and cache queues
// BEFORE Close. Close does not cancel admitted work; its context bounds waiting,
// not ownership of the single retained close operation.
type independentOwnerLock struct {
	connection *sql.Conn
	key        string
}

type IndependentStorage struct {
	business                     *Actions
	syncCache                    *ProtocolCache
	businessBackend, syncBackend Backend
	cancel                       context.CancelFunc
	release                      func()
	closeOnce                    sync.Once
	closeDone                    chan struct{}
	ownerLocks                   []independentOwnerLock
	closeErr                     error
	managed                      *ManagedFinite
}

func (s *IndependentStorage) ManagedFinite() *ManagedFinite { return s.managed }

func (s *IndependentStorage) BusinessActions() *Actions { return s.business }
func (s *IndependentStorage) BusinessStore() *Store     { return s.business.s }
func (s *IndependentStorage) SyncCache() *ProtocolCache { return s.syncCache }

func (s *IndependentStorage) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		go func() {
			defer close(s.closeDone)
			if s.syncCache != nil {
				_ = s.syncCache.Close()
			}
			var errs []error
			if s.managed != nil {
				errs = append(errs, s.managed.close())
			}
			for _, lock := range s.ownerLocks {
				_, err := lock.connection.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1,0))", lock.key)
				errs = append(errs, err)
			}
			if s.syncBackend != nil {
				errs = append(errs, s.syncBackend.Close())
			}
			if s.businessBackend != nil {
				errs = append(errs, s.businessBackend.Close())
			}
			if s.cancel != nil {
				s.cancel()
			}
			if s.release != nil {
				s.release()
			}
			s.closeErr = errors.Join(errs...)
		}()
	})
	select {
	case <-s.closeDone:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func independentNames(o IndependentStorageOptions) error {
	if o.BusinessNamespace == "" || o.SyncNamespace == "" {
		return fmt.Errorf("independent storage requires explicit namespace identities")
	}
	if _, err := describeStore(nil, o.Schema, o.BusinessNamespace, o.Ordering, true); err != nil {
		return err
	}
	if _, err := describeStore(nil, schema.Schema{}, o.SyncNamespace, o.Ordering, false); err != nil {
		return err
	}
	for _, a := range independentNamespaceFamily(o.BusinessNamespace) {
		for _, b := range independentNamespaceFamily(o.SyncNamespace) {
			if a == b {
				return fmt.Errorf("independent storage namespace families overlap at %q", a)
			}
		}
	}
	return nil
}
func independentNamespaceFamily(n string) []string {
	return []string{n, n + "_sync", n + "_legacy", n + "_rpc_1"}
}

// OpenIndependentPostgres always opens two distinct pools, even for the same URL.
// No arbitrary Backend pair can be supplied as a purported isolation proof.
func OpenIndependentPostgres(ctx context.Context, o IndependentStorageOptions) (*IndependentStorage, error) {
	return openIndependentPostgres(ctx, o, nil)
}
func openIndependentPostgres(ctx context.Context, o IndependentStorageOptions, managed *ManagedFiniteOptions) (*IndependentStorage, error) {
	if err := independentNames(o); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o.PostgresURL == "" {
		return nil, fmt.Errorf("independent PostgreSQL storage requires a URL")
	}
	s := &IndependentStorage{closeDone: make(chan struct{})}
	var err error
	s.businessBackend, err = OpenPostgres(ctx, o.PostgresURL)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*IndependentStorage, error) {
		return nil, errors.Join(err, s.Close(context.Background()))
	}
	// A distinct name is not proof of a distinct advisory-lock identity. Compare
	// the exact server-side hashes used by Store and SyncMigrator before any DDL.
	for _, a := range []string{o.BusinessNamespace, o.BusinessNamespace + ":sync-migration"} {
		for _, b := range []string{o.SyncNamespace, o.SyncNamespace + ":sync-migration"} {
			r, e := s.businessBackend.Query(ctx, "SELECT hashtextextended($1,0)=hashtextextended($2,0) AS collision", a, b)
			if e != nil {
				return fail(e)
			}
			if len(r.Rows) != 1 || r.Rows[0]["collision"] != false {
				return fail(fmt.Errorf("independent storage advisory-lock identities collide"))
			}
		}
	}
	// Lifetime locks exclude other cooperative owners, not ordinary per-batch
	// transaction locks. Use one dedicated connection so pool scheduling cannot
	// release or accidentally reacquire a session-scoped lock.
	pool := s.businessBackend.(*pgBackend)
	businessConnection, err := pool.db.Conn(ctx)
	if err != nil {
		return fail(err)
	}
	s.businessBackend = &independentPGBackend{pgBackend: pool, connection: businessConnection}
	s.syncBackend, err = OpenPostgres(ctx, o.PostgresURL)
	if err != nil {
		return fail(err)
	}
	syncPool := s.syncBackend.(*pgBackend)
	syncConnection, err := syncPool.db.Conn(ctx)
	if err != nil {
		return fail(err)
	}
	s.syncBackend = &independentPGBackend{pgBackend: syncPool, connection: syncConnection}
	operational := []string{o.BusinessNamespace, o.BusinessNamespace + ":sync-migration", o.SyncNamespace, o.SyncNamespace + ":sync-migration"}
	allKeys := []string{}
	for _, domain := range []struct {
		connection *sql.Conn
		root       string
	}{{businessConnection, o.BusinessNamespace}, {syncConnection, o.SyncNamespace}} {
		for _, root := range independentNamespaceFamily(domain.root) {
			key := root + ":independent-owner"
			for _, other := range append(operational, allKeys...) {
				var collision bool
				if e := domain.connection.QueryRowContext(ctx, "SELECT hashtextextended($1,0)=hashtextextended($2,0)", key, other).Scan(&collision); e != nil {
					return fail(e)
				}
				if collision {
					return fail(fmt.Errorf("independent owner advisory-lock identities collide"))
				}
			}
			var acquired bool
			if e := domain.connection.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&acquired); e != nil {
				return fail(e)
			}
			if !acquired {
				return fail(fmt.Errorf("independent storage namespace already has an active owner"))
			}
			s.ownerLocks = append(s.ownerLocks, independentOwnerLock{connection: domain.connection, key: key})
			allKeys = append(allKeys, key)
		}
	}
	if managed != nil && managed.ViewsSchema != "" {
		if err = s.reserveManagedAlias(ctx, managed.ViewsSchema, append(operational, allKeys...)); err != nil {
			return fail(err)
		}
	}
	if err = s.initializeMode(ctx, o, managed); err != nil {
		return fail(err)
	}
	return s, nil
}

// OpenIndependentPGlite uses separate processes and databases. Unlike the pinned
// source's single PGlite callback-only transaction, native business SQL remains
// atomic and sync persistence is independent. Initialization cancellation stops
// initialization, but a successful owner's process lifetime is not tied to ctx.
func OpenIndependentPGlite(ctx context.Context, o IndependentStorageOptions) (*IndependentStorage, error) {
	return openIndependentPGlite(ctx, o, nil)
}
func openIndependentPGlite(ctx context.Context, o IndependentStorageOptions, managed *ManagedFiniteOptions) (*IndependentStorage, error) {
	if err := independentNames(o); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a, err := canonicalIndependentDirectory(o.BusinessDirectory)
	if err != nil {
		return nil, err
	}
	b, err := canonicalIndependentDirectory(o.SyncDirectory)
	if err != nil {
		return nil, err
	}
	releaseLocal, err := reserveIndependentDirectories(a, b)
	if err != nil {
		return nil, err
	}
	releaseFiles, inheritedLocks, err := reserveIndependentFilesystem(a, b)
	if err != nil {
		releaseLocal()
		return nil, err
	}
	release := func() { releaseFiles(); releaseLocal() }
	lifetime, cancel := context.WithCancel(context.Background())
	s := &IndependentStorage{cancel: cancel, release: release, closeDone: make(chan struct{})}
	cancelDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { cancel(); close(cancelDone) })
	var stopOnce sync.Once
	stopWatcher := func() {
		stopOnce.Do(func() {
			if !stop() {
				<-cancelDone
			}
		})
	}
	defer stopWatcher()
	fail := func(err error) (*IndependentStorage, error) {
		cancel()
		return nil, errors.Join(err, s.Close(context.Background()))
	}
	s.businessBackend, err = openPGlite(lifetime, a, inheritedLocks)
	if err != nil {
		return fail(err)
	}
	s.syncBackend, err = openPGlite(lifetime, b, inheritedLocks)
	if err != nil {
		return fail(err)
	}
	if err = s.initializeMode(ctx, o, managed); err != nil {
		return fail(err)
	}
	stopWatcher()
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *IndependentStorage) initialize(ctx context.Context, o IndependentStorageOptions) error {
	return s.initializeMode(ctx, o, nil)
}
func (s *IndependentStorage) initializeMode(ctx context.Context, o IndependentStorageOptions, managed *ManagedFiniteOptions) error {
	var err error
	if managed == nil {
		s.business, err = NewActions(ctx, s.businessBackend, o.Schema, o.BusinessNamespace, o.Ordering)
	} else {
		s.managed, err = s.initializeManagedFinite(ctx, o, *managed)
		if err == nil {
			s.business = &Actions{s.managed.s}
			s.managed.start()
		}
	}
	if err != nil {
		return err
	}
	syncStore, err := New(ctx, s.syncBackend, schema.Schema{}, o.SyncNamespace, o.Ordering)
	if err != nil {
		return err
	}
	s.syncCache, err = syncStore.EnableProtocolCache(ctx)
	if s.managed != nil {
		if err == nil {
			err = s.managed.requireLease(ctx, s.managed.reader())
		}
	}
	return err
}

func canonicalIndependentDirectory(path string) (string, error) {
	if path == "" || path == "memory://" {
		return "memory://", nil
	}
	if strings.Contains(path, "://") {
		return "", fmt.Errorf("PGlite storage requires a filesystem directory or memory://")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	probe := absolute
	var missing []string
	for {
		resolved, e := filepath.EvalSymlinks(probe)
		if e == nil {
			info, e := os.Stat(resolved)
			if e != nil {
				return "", e
			}
			if !info.IsDir() {
				return "", fmt.Errorf("PGlite ancestor %q is not a directory", probe)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			if resolved == string(filepath.Separator) {
				return "", fmt.Errorf("PGlite directory cannot be filesystem root")
			}
			return resolved, nil
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		// Do not treat a dangling symlink as a missing ordinary directory.
		if _, lerr := os.Lstat(probe); lerr == nil {
			return "", fmt.Errorf("PGlite directory has a dangling symlink at %q", probe)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", e
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}
func independentDirectoriesOverlap(a, b string) bool {
	if a == "memory://" || b == "memory://" {
		return false
	}
	if a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator)) {
		return true
	}
	ai, ae := os.Stat(a)
	bi, be := os.Stat(b)
	return ae == nil && be == nil && os.SameFile(ai, bi)
}

var independentDirectories = struct {
	sync.Mutex
	paths map[string]bool
}{paths: map[string]bool{}}

func reserveIndependentDirectories(a, b string) (func(), error) {
	independentDirectories.Lock()
	defer independentDirectories.Unlock()
	if independentDirectoriesOverlap(a, b) {
		return nil, fmt.Errorf("independent PGlite directories must not be equal or nested")
	}
	for _, p := range []string{a, b} {
		for active := range independentDirectories.paths {
			if independentDirectoriesOverlap(p, active) {
				return nil, fmt.Errorf("PGlite directory overlaps an active independent owner: %q", p)
			}
		}
	}
	for _, p := range []string{a, b} {
		if p != "memory://" {
			independentDirectories.paths[p] = true
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			independentDirectories.Lock()
			defer independentDirectories.Unlock()
			delete(independentDirectories.paths, a)
			delete(independentDirectories.paths, b)
		})
	}, nil
}
