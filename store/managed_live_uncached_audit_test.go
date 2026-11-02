package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Recompute every public ledger hash to prove policy validation, not a stale
// checksum, rejects an enabled-chain intent claiming a disabled-chain deletion.
func TestManagedLiveUncachedPrepareAudit(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, opts, open := continuousStoreFixture(t, dialect)
			opts.CacheRPCRequests = map[uint64]bool{1: false, 10: true}
			d := open(opts)
			io, scope, err := d.NewRuntimeScope(ctx)
			if err != nil {
				t.Fatal(err)
			}
			batch := liveSegmentsUnitBatch(d.live())
			a, err := d.RetainBatch(ctx, batch)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.ApplyRuntimeBatch(io, batch, a, nil); err != nil {
				t.Fatal(err)
			}
			continuousStoreCacheSeed(t, d, io)
			if err = scope.Seal(ctx); err != nil {
				t.Fatal(err)
			}
			c, err := d.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			request := ManagedLiveDomainReorg{Reorg: ManagedLiveReorg{ID: "cache-policy-audit", Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Ancestor: batch.Baselines[10]}, Orphaned: []ManagedLiveHead{batch.Blocks[0].Head, batch.Blocks[2].Head}}
			if _, err = d.CoordinateReorg(ctx, request); err != nil {
				t.Fatal(err)
			}
			liveDomainsCacheCount(t, d, 10, 1)
			liveDomainsCacheCount(t, d, 1, 3)
			l, err := d.readLedger(ctx, d.storage.syncBackend)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = d.audit(ctx); err != nil {
				t.Fatal("valid cached-chain proof rejected", err)
			}
			prior := ""
			intentHash := ""
			for i, record := range l.Records {
				if i == 0 {
					prior = record.Previous
				}
				record.Previous = prior
				if record.Kind == "prepare" {
					var intent liveDomainIntent
					if err = json.Unmarshal([]byte(record.Payload), &intent); err != nil {
						t.Fatal(err)
					}
					intent.Deleted = append(intent.Deleted, `{"chain_id":1,"block_number":5,"request_hash":"forged","result":"0xdead"}`)
					intentHash = managedHash(intent)
					raw, e := json.Marshal(intent)
					if e != nil {
						t.Fatal(e)
					}
					record.Payload = string(raw)
				}
				if record.Kind == "ack" {
					var ack liveDomainAck
					if err = json.Unmarshal([]byte(record.Payload), &ack); err != nil {
						t.Fatal(err)
					}
					ack.IntentHash = intentHash
					raw, e := json.Marshal(ack)
					if e != nil {
						t.Fatal(e)
					}
					record.Payload = string(raw)
				}
				record.Hash = ""
				record.Hash = managedHash(record)
				if err = d.storage.syncBackend.Exec(ctx, "UPDATE "+d.table("log")+" SET previous=$1,payload=$2,hash=$3 WHERE sequence=$4", record.Previous, record.Payload, record.Hash, record.Sequence); err != nil {
					t.Fatal(err)
				}
				prior = record.Hash
			}
			if err = d.storage.syncBackend.Exec(ctx, "UPDATE "+d.table("head")+" SET hash=$1 WHERE key='head'", prior); err != nil {
				t.Fatal(err)
			}
			if _, err = d.readLedger(ctx, d.storage.syncBackend); err != nil {
				t.Fatal("tamper fixture failed ordinary ledger hash validation", err)
			}
			if _, _, _, err = d.audit(ctx); !errors.Is(err, ErrManagedLiveReceiptConflict) {
				t.Fatalf("disabled-row deletion proof accepted after valid hash rewrite: %v", err)
			}
		})
	}
}
