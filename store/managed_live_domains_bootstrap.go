package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"ponder.local/ponder-go/schema"
)

type liveDomainBinding struct{ Identity, Operation, Hash, CacheCatalog string }

func domainPriorOwner(s *IndependentStorage, o IndependentStorageOptions, opts ManagedLiveOptions) (*ManagedLiveDomains, error) {
	st, err := describeStore(s.businessBackend, cloneManagedSchema(o.Schema), o.BusinessNamespace, schema.Multichain, true)
	if err != nil {
		return nil, err
	}
	st.viewsSchema = opts.ViewsSchema
	raw, err := json.Marshal(struct {
		Build, Plan, Alias, Ordering, Sync string
		Schema                             schema.Schema
		Chains                             []MigrationChain
		Ends                               map[uint64]uint64
	}{opts.BuildID, opts.PlanID, opts.ViewsSchema, string(schema.Multichain), o.SyncNamespace, o.Schema, opts.Chains, opts.EndBlocks})
	if err != nil {
		return nil, err
	}
	if opts.liveRuntime {
		raw, err = runtimeOptionsIdentityPayload(raw, opts)
		if err != nil {
			return nil, err
		}
	}
	digest := sha256.Sum256(append([]byte((&ManagedLive{opts: opts}).version()+"\x00"), raw...))
	m := &ManagedLive{owner: &ManagedFinite{s: st, identity: hex.EncodeToString(digest[:]), opts: opts.ManagedFiniteOptions}, opts: opts}
	st.managedLive = m
	syncStore, err := describeStore(s.syncBackend, schema.Schema{}, o.SyncNamespace, o.Ordering, false)
	if err != nil {
		return nil, err
	}
	shadow := &IndependentStorage{businessBackend: s.businessBackend, syncBackend: s.syncBackend, live: m, syncCache: &ProtocolCache{s: syncStore, namespace: o.SyncNamespace + "_rpc_1"}}
	return &ManagedLiveDomains{storage: shadow, spec: liveDomainSpec(o, opts)}, nil
}

// Bootstrap the native sync ownership metadata AND protocol marker in one
// transaction. A pre-genesis empty marker can retry initialization; a bound
// domain may never reconstruct its missing peer or missing protocol evidence.
func prepareLiveDomains(ctx context.Context, s *IndependentStorage, o IndependentStorageOptions, opts ManagedLiveOptions) error {
	d, err := domainPriorOwner(s, o, opts)
	if err != nil {
		return err
	}
	cache := d.storage.syncCache
	b := s.syncBackend
	legacy, err := s.syncBackend.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", o.SyncNamespace+"_legacy")
	if err != nil {
		return err
	}
	if len(legacy.Rows) != 0 {
		return ErrManagedLiveReceiptConflict
	}
	business, err := s.businessBackend.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", o.BusinessNamespace)
	if err != nil {
		return err
	}
	hasBusiness := len(business.Rows) > 0
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = cache.s.lock(ctx, tx); err != nil {
		return err
	}
	r, err := tx.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", o.SyncNamespace)
	if err != nil {
		return err
	}
	if len(r.Rows) > 0 {
		l, e := d.readLedger(ctx, tx)
		if e != nil {
			return e
		}
		if !hasBusiness {
			if l.Binding != nil || len(l.Records) > 0 {
				return ErrManagedLiveReceiptConflict
			}
			return nil
		}
		// Both lifetime owner locks are already held. Authenticate the entire prior
		// business snapshot and every cross-domain reference BEFORE recovery writes.
		bt, e := s.businessBackend.Begin(ctx)
		if e != nil {
			return e
		}
		defer bt.Rollback(context.Background())
		if e = d.live().owner.s.lock(ctx, bt); e != nil {
			return e
		}
		prior, _, e := d.live().readDurableState(ctx, bt, true)
		if e != nil {
			if opts.liveRuntime {
				return fmt.Errorf("runtime preopen durable: %w", e)
			}
			return e
		}
		if e = d.checkBinding(l, prior); e != nil {
			if opts.liveRuntime {
				return fmt.Errorf("runtime preopen binding: %w", e)
			}
			return e
		}
		for id, ack := range l.Acks {
			if e = d.checkAck(l.Intents[id], ack, prior); e != nil {
				return e
			}
		}
		if e = d.checkRuntimeLedger(l, prior); e != nil {
			return fmt.Errorf("runtime preopen ledger: %w", e)
		}
		if l.Pending != "" {
			if _, _, e = d.domainProof(l.Intents[l.Pending], prior); e != nil {
				return e
			}
		}
		return nil
	}
	if hasBusiness {
		return ErrManagedLiveReceiptConflict
	}
	if err = cache.s.initializeInternal(ctx, tx); err != nil {
		return err
	}
	if err = cache.s.createUserObjects(ctx, tx, true); err != nil {
		return err
	}
	head, log := d.table("head"), d.table("log")
	for _, sql := range []string{"CREATE TABLE " + head + "(key text PRIMARY KEY CHECK(key='head'),spec text NOT NULL,sequence bigint NOT NULL,hash text NOT NULL,catalog text NOT NULL)", "CREATE TABLE " + log + "(sequence bigint PRIMARY KEY,kind text NOT NULL,id text NOT NULL,previous text NOT NULL,payload text NOT NULL,hash text NOT NULL,UNIQUE(kind,id))"} {
		if err = tx.Exec(ctx, sql); err != nil {
			return err
		}
	}
	catalog, err := liveDomainCatalog(ctx, tx, o.SyncNamespace)
	if err != nil {
		return err
	}
	if err = tx.Exec(ctx, "INSERT INTO "+head+" VALUES('head',$1,0,$2,$3)", d.spec, managedHash(d.spec), catalog); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *ManagedLiveDomains) checkBinding(l liveDomainLedger, b managedLiveDurable) error {
	ordered, err := orderLiveOperations(b.Operations, b.Control.LastOperation)
	if err != nil || len(ordered) == 0 {
		return errors.Join(ErrManagedLiveReceiptConflict, err)
	}
	if l.Binding == nil {
		for _, op := range ordered {
			if op.Kind != "open" {
				return ErrManagedLiveReceiptConflict
			}
		}
		return nil
	}
	first := ordered[0]
	bind := l.Binding
	if bind.Identity != d.live().owner.identity || first.Kind != "open" || first.ID != bind.Operation || first.AfterHash != bind.Hash {
		return ErrManagedLiveReceiptConflict
	}
	return nil
}

func (d *ManagedLiveDomains) bindDomains(ctx context.Context, l liveDomainLedger, b managedLiveDurable) error {
	if err := d.checkBinding(l, b); err != nil {
		return err
	}
	if l.Binding != nil {
		return nil
	}
	ordered, err := orderLiveOperations(b.Operations, b.Control.LastOperation)
	if err != nil {
		return err
	}
	catalog, err := liveDomainSyncEnvelope(ctx, d.storage.syncBackend, d.storage.syncCache.s.Namespace)
	if err != nil {
		return err
	}
	binding := liveDomainBinding{Identity: d.live().owner.identity, Operation: ordered[0].ID, Hash: ordered[0].AfterHash, CacheCatalog: catalog}
	err = d.storage.syncCache.transaction(ctx, func(tx Transaction) error {
		current, e := d.readLedger(ctx, tx)
		if e != nil {
			return e
		}
		if len(current.Records) != 0 {
			return ErrManagedLiveReceiptConflict
		}
		return d.appendLedger(ctx, tx, current, "bind", binding.Operation, binding)
	})
	if err == nil {
		return nil
	}
	actual, e := d.readLedger(ctx, d.storage.syncBackend)
	if e == nil && actual.Binding != nil && managedHash(actual.Binding) == managedHash(binding) {
		return nil
	}
	d.uncertain = true
	return &ManagedStageError{Stage: "domains-bind", Outcome: "unknown", Err: errors.Join(err, e, fmt.Errorf("exact domain binding not confirmed"))}
}
