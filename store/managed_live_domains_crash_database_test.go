package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type liveDomainCrashBackend struct {
	Backend
	phase string
}
type liveDomainCrashTx struct {
	Transaction
	b      *liveDomainCrashBackend
	target bool
}

func (b *liveDomainCrashBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &liveDomainCrashTx{Transaction: tx, b: b}, nil
}
func (tx *liveDomainCrashTx) Exec(ctx context.Context, q string, args ...any) error {
	if (strings.HasPrefix(q, "INSERT INTO") && strings.Contains(q, "_ponder_go_domains_log") && len(args) > 1 && args[1] == tx.b.phase) || (tx.b.phase == "business" && strings.Contains(q, "_ponder_go_live_operations") && strings.Contains(q, "'reorg'")) {
		tx.target = true
	}
	return tx.Transaction.Exec(ctx, q, args...)
}
func (tx *liveDomainCrashTx) Commit(ctx context.Context) error {
	if e := tx.Transaction.Commit(ctx); e != nil {
		return e
	}
	if tx.target {
		fmt.Println("DOMAINS_APP_EXIT_BOUNDARY " + tx.b.phase)
		os.Exit(107)
	}
	return nil
}

func TestManagedLiveDomainsPostgresCrash(t *testing.T) {
	ctx := context.Background()
	if phase := os.Getenv("PONDER_M45_DOMAINS_CRASH"); phase != "" {
		var spec struct {
			Options IndependentStorageOptions
			Live    ManagedLiveOptions
		}
		if e := json.Unmarshal([]byte(os.Getenv("PONDER_M45_DOMAINS_SPEC")), &spec); e != nil {
			t.Fatal(e)
		}
		d, e := OpenManagedLiveDomainsPostgres(ctx, spec.Options, spec.Live)
		if e != nil {
			t.Fatal(e)
		}
		_, r := liveDomainsSeed(t, d)
		liveDomainsCacheSeed(t, d)
		public, e := checkpointStates(ctx, d.storage.businessBackend, d.storage.BusinessStore())
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(public)
		fmt.Printf("DOMAINS_PUBLIC %s\n", raw)
		owners := []managedOwnerIdentity{}
		for _, lock := range d.storage.ownerLocks {
			row, e := d.storage.businessBackend.Query(ctx, `SELECT a.pid::text AS pid,a.backend_start::text AS started FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND l.granted AND l.objsubid=1 AND l.classid=((hashtextextended($1,0)>>32)&4294967295)::oid AND l.objid=(hashtextextended($1,0)&4294967295)::oid AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database())`, lock.key)
			if e != nil || len(row.Rows) != 1 {
				t.Fatalf("owned lock %v %v", row, e)
			}
			pid, e := liveInt(row.Rows[0], "pid")
			if e != nil {
				t.Fatal(e)
			}
			owners = append(owners, managedOwnerIdentity{Key: lock.key, PID: int(pid), Started: fmt.Sprint(row.Rows[0]["started"])})
		}
		raw, _ = json.Marshal(owners)
		fmt.Printf("DOMAINS_OWNERS %s\n", raw)
		if phase == "business" {
			d.live().owner.s.Backend = &liveDomainCrashBackend{Backend: d.live().owner.s.Backend, phase: phase}
		} else {
			b := &liveDomainCrashBackend{Backend: d.storage.syncBackend, phase: phase}
			d.storage.syncBackend = b
			d.storage.syncCache.s.Backend = b
		}
		_, e = d.CoordinateReorg(ctx, r)
		t.Fatalf("application exit not reached: %v", e)
		return
	}
	for _, phase := range []string{"prepare", "business", "ack"} {
		t.Run(phase, func(t *testing.T) {
			o, opts, open := liveDomainsFixture(t, "postgres")
			spec, _ := json.Marshal(struct {
				Options IndependentStorageOptions
				Live    ManagedLiveOptions
			}{o, opts})
			binary, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(binary, "-test.run=^TestManagedLiveDomainsPostgresCrash$", "-test.v", "-test.timeout=120s")
			cmd.Env = append(os.Environ(), "PONDER_M45_DOMAINS_CRASH="+phase, "PONDER_M45_DOMAINS_SPEC="+string(spec))
			trace, e := cmd.CombinedOutput()
			t.Logf("actual application child output:\n%s", trace)
			var exited *exec.ExitError
			if !errors.As(e, &exited) || exited.ExitCode() != 107 || !strings.Contains(string(trace), "DOMAINS_APP_EXIT_BOUNDARY "+phase) {
				t.Fatalf("wrong application exit: %v", e)
			}
			var owners []managedOwnerIdentity
			var public []CheckpointState
			for _, line := range strings.Split(string(trace), "\n") {
				if strings.HasPrefix(line, "DOMAINS_OWNERS ") {
					if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "DOMAINS_OWNERS ")), &owners); e != nil {
						t.Fatal(e)
					}
				}
				if strings.HasPrefix(line, "DOMAINS_PUBLIC ") {
					if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "DOMAINS_PUBLIC ")), &public); e != nil {
						t.Fatal(e)
					}
				}
			}
			if len(owners) != 9 || len(public) != 2 {
				t.Fatal("missing actual owner/public evidence")
			}
			probe := liveBatchPGProbe(t, o)
			deadline := time.Now().Add(5 * time.Second)
			for {
				left := 0
				for _, owner := range owners {
					r, e := probe.Query(ctx, "SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND backend_start::text=$2", owner.PID, owner.Started)
					if e != nil {
						t.Fatal(e)
					}
					left += len(r.Rows)
				}
				if left == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("owned application sessions still alive")
				}
				time.Sleep(10 * time.Millisecond)
			}
			opts.OpenID = "reopen-after-domain-exit"
			d := open(opts)
			liveDomainsCacheCount(t, d, 10, 1)
			liveDomainsCacheCount(t, d, 1, 3)
			liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "missing")
			after, e := checkpointStates(ctx, d.storage.businessBackend, d.storage.BusinessStore())
			if e != nil || managedHash(after) != managedHash(public) {
				t.Fatalf("crash public changed: %v %v", after, e)
			}
			l, e := d.readLedger(ctx, d.storage.syncBackend)
			if e != nil || l.Pending != "" || len(l.Acks) != 1 {
				t.Fatalf("restart ledger=%+v %v", l, e)
			}
			ack := l.Acks["domains-reorg"]
			want := "covered-by-recovery"
			if phase == "ack" {
				want = "reorg"
			}
			if ack.Result.Outcome != want {
				t.Fatalf("restart result=%+v", ack)
			}
			// Actual new-generation replay uses newly retained inputs. No old handler or
			// requester is reused; the public head remains held until this real commit.
			c, e := d.Control(ctx)
			if e != nil {
				t.Fatal(e)
			}
			batch := liveSegmentsUnitBatch(d.live())
			batch.ID = "domains-crash-replay"
			batch.Generation = c.Generation
			batch.ExpectedLastOperation = c.LastOperation
			batch.ExpectedTail = c.Tail
			batch.Baselines = nil
			a, e := d.RetainBatch(ctx, batch)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			var saved *Writer
			if _, e = d.ApplyBatch(ctx, batch, a, liveBatchPGSharedHandler(t, &calls, &saved)); e != nil || calls != 3 {
				t.Fatalf("actual replay calls=%d %v", calls, e)
			}
			liveBatchPGBalance(t, d.storage.businessBackend, d.storage.BusinessStore(), "20")
			if ready, e := d.Ready(ctx); e != nil || ready {
				t.Fatalf("runtime readiness invented: %v %v", ready, e)
			}
		})
	}
}
