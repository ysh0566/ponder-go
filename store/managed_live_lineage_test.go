package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func sparseRangeHead(chain, number uint64) ManagedLiveHead {
	return ManagedLiveHead{Checkpoint: checkpoint.MustEncode(checkpoint.Block(1000+number/2, chain, number, true)), Hash: fmt.Sprintf("0x%064x", chain*100000+number), ParentHash: fmt.Sprintf("0x%064x", chain*100000+number-1)}
}

func TestLogOnlyLineageRangeDatabase(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "business")
			open := func() Backend {
				var b Backend
				var err error
				if dialect == "postgres" {
					url := os.Getenv("PONDER_TEST_POSTGRES")
					if url == "" {
						t.Fatal("explicit test PostgreSQL URL required")
					}
					b, err = OpenPostgres(ctx, url)
				} else {
					b, err = OpenPGlite(ctx, dir)
				}
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			b := open()
			s := &Store{Namespace: fmt.Sprintf("ponder_go_lineage_%x", time.Now().UnixNano())}
			namespace := schema.Quote(s.Namespace)
			if err := b.Exec(ctx, "CREATE SCHEMA "+namespace); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := b.Exec(ctx, "DROP SCHEMA "+namespace+" CASCADE"); err != nil {
					t.Error(err)
				}
				if err := b.Close(); err != nil {
					t.Error(err)
				}
			})
			begin := func() Transaction {
				tx, err := b.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { tx.Rollback(ctx) })
				return tx
			}
			tx := begin()
			if err := s.createLineagePages(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			read := func(chain, n uint64) (ManagedLiveHead, error) { return sparseRangeHead(chain, n), nil }
			const first, last = 13, 600
			write := func(tx Transaction, chain uint64, read ManagedLiveHeaderReader) (managedLineageRange, error) {
				return s.writeLineageRange(ctx, tx, "source-group", sparseRangeHead(chain, first), sparseRangeHead(chain, last), read)
			}
			tx = begin()
			if _, err := write(tx, 1, read); err != nil {
				t.Fatal(err)
			}
			bad := func(chain, n uint64) (ManagedLiveHead, error) {
				h := sparseRangeHead(chain, n)
				if n == first+lineagePageSize {
					h.ParentHash = h.Hash
				}
				return h, nil
			}
			if _, err := write(tx, 2, bad); !errors.Is(err, ErrManagedLiveBatchConflict) {
				t.Fatalf("broken link across pages: %v", err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			r, err := b.Query(ctx, "SELECT count(*)::text AS count FROM "+s.internal("_ponder_go_live_lineage_pages"))
			if err != nil || len(r.Rows) != 1 || r.Rows[0]["count"] != "0" {
				t.Fatalf("partial group after rollback: %v (%v)", r, err)
			}
			tx = begin()
			proof, err := write(tx, 1, read)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			b = open()
			if err := s.verifyLineageRange(ctx, b, "source-group", proof); err != nil {
				t.Fatal(err)
			}
			wrong := proof
			wrong.Digest = managedHash("different range")
			if err := s.verifyLineageRange(ctx, b, "source-group", wrong); !errors.Is(err, ErrManagedLiveBatchConflict) {
				t.Fatalf("accepted changed digest: %v", err)
			}
			if err := s.verifyLineageRange(ctx, b, "stale-group", proof); !errors.Is(err, ErrManagedLiveBatchConflict) {
				t.Fatalf("accepted stale range: %v", err)
			}
			for name, alter := range map[string]string{
				"missing":           "DELETE FROM " + s.internal("_ponder_go_live_lineage_pages") + " WHERE first_block=269",
				"extra":             "INSERT INTO " + s.internal("_ponder_go_live_lineage_pages") + " SELECT operation_id,chain_id,14,headers FROM " + s.internal("_ponder_go_live_lineage_pages") + " WHERE first_block=13",
				"cross-page-parent": "UPDATE " + s.internal("_ponder_go_live_lineage_pages") + " SET headers=jsonb_set(headers,'{0,parentHash}',headers->0->'hash') WHERE first_block=269",
				"reordered":         "UPDATE " + s.internal("_ponder_go_live_lineage_pages") + " SET headers=jsonb_set(headers,'{0}',headers->1) WHERE first_block=269",
			} {
				t.Run(name, func(t *testing.T) {
					tx, err := b.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)
					if err := tx.Exec(ctx, alter); err != nil {
						t.Fatal(err)
					}
					if err := s.verifyLineageRange(ctx, tx, "source-group", proof); !errors.Is(err, ErrManagedLiveBatchConflict) {
						t.Fatalf("accepted damaged range: %v", err)
					}
				})
			}
		})
	}
}

func TestLogOnlyPagedReceiptIsolation(t *testing.T) {
	for _, test := range []struct{ kind, wire string }{
		{"batch", `{"gapScans":null}`},
		{"batch", `{"Batch":{"Blocks":[{"rAnGeFiRsT":null}]}}`},
		{"batch", `{"segments":[{"block":{"rangeDigest":""}}]}`},
		{"finalize", `{"pruned":[{"block":{"rangeFirst":{}}}]}`},
		{"reorg", `{"removed":[{"block":{"rangeDigest":null}}]}`},
		{"recover", `{"removed":[{"block":{"rangeDigest":null}}]}`},
		{"serve", `{"LogOnlyScan":{"1":{"scannedRange":null}}}`},
		{"serve", `{"LogOnlyScan":{"1":{"finalTailRange":null}}}`},
	} {
		if err := rejectPagedReceipt(managedLiveOperation{Kind: test.kind, Payload: []byte(test.wire)}); !errors.Is(err, ErrManagedLiveBatchConflict) {
			t.Errorf("accepted old %s receipt %s: %v", test.kind, test.wire, err)
		}
	}
	if err := rejectPagedReceipt(managedLiveOperation{Kind: "batch", Payload: []byte(`{"journal":[{"row":{"rangeFirst":1,"rangeDigest":"user value"}}]}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestLogOnlyPagedScanBoundaries(t *testing.T) {
	get := func(id, n uint64) (ManagedLiveHead, error) { return sparseRangeHead(id, n), nil }
	m, c, _ := logOnlyScanFixture(t)
	m.opts.EndBlocks[1] = 13000
	c.HistoryCuts[1] = m.opts.EndBlocks[1]
	c.Cursors[1] = sparseRangeHead(1, 12990)
	c.FinalizedHeads[1] = sparseRangeHead(1, 13002)
	scans, err := m.acquiredScanRanges(context.Background(), c, get)
	if err != nil {
		t.Fatal(err)
	}
	base := scans[1]
	end, err := base.pagedScanEnd(c, 1)
	if err != nil || end != sparseRangeHead(1, 13000) {
		t.Fatalf("scan endpoint %v %v", end, err)
	}
	for _, name := range []string{"missing-tail", "wrong-cursor", "wrong-cut", "wrong-final", "broken-parent", "repeated-boundary", "foreign-chain", "empty-array", "legacy-array"} {
		t.Run(name, func(t *testing.T) {
			a, b := *base.Scanned, *base.FinalTail
			scan := managedLogOnlyScan{Scanned: &a, FinalTail: &b}
			switch name {
			case "missing-tail":
				scan.FinalTail = nil
			case "wrong-cursor":
				a.First = sparseRangeHead(1, 12989)
			case "wrong-cut":
				a.Last = sparseRangeHead(1, 13001)
			case "wrong-final":
				b.Last = sparseRangeHead(1, 13003)
			case "broken-parent":
				b.First.ParentHash = b.First.Hash
			case "repeated-boundary":
				b.First = a.Last
			case "foreign-chain":
				a.Last = sparseRangeHead(2, 13000)
			case "empty-array":
				scan.Lineage = []ManagedLiveHead{}
			case "legacy-array":
				scan.Lineage = []ManagedLiveHead{a.First}
			}
			if _, err := scan.pagedScanEnd(c, 1); !errors.Is(err, ErrManagedLiveBatchConflict) {
				t.Fatalf("invalid range accepted: %v", err)
			}
		})
	}
}

func TestLogOnlyPagedFormatSelection(t *testing.T) {
	m, _, _ := logOnlyScanFixture(t)
	m.opts.Chains = []MigrationChain{{ID: 1}, {ID: 2}}
	m.opts.runtimeStarts = map[uint64]uint64{1: 3, 2: 10}
	limit := uint64(ManagedLiveOmnichainBlockLimit(2))
	m.opts.EndBlocks = map[uint64]uint64{1: 2 + limit/2, 2: 9 + limit/2}
	if m.version() != managedLivePagedLogOnlyVersion {
		t.Fatal("old boundary changed format")
	}
	m.opts.EndBlocks[2]++
	if m.version() != managedLivePagedLogOnlyVersion {
		t.Fatal("long range did not select the current format")
	}
	m.opts.runtimeLogFilters = nil
	if m.version() != managedLiveOmnichainVersion {
		t.Fatal("closing block callbacks selected a log-only format")
	}
}
