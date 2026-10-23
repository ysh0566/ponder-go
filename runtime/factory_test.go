package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"sync"
	"testing"
	"time"
)

// This is additional native integration evidence, not a replacement for any
// upstream factory/E2E test. RPC responses are deterministic; both databases are real.
func TestFactoryRuntime(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			directory := t.TempDir() + "/pglite"
			open := func() store.Backend {
				t.Helper()
				var b store.Backend
				var e error
				if dialect == "postgres" {
					url := os.Getenv("PONDER_TEST_POSTGRES")
					if url == "" {
						t.Skip("explicit task database required; no parity credit")
					}
					b, e = store.OpenPostgres(ctx, url)
				} else {
					b, e = store.OpenPGlite(ctx, directory)
				}
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			backend := open()
			defer func() { backend.Close() }()
			model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
			namespace := fmt.Sprintf("ponder_go_factory_%x", time.Now().UnixNano())
			newStore := func() *store.Store {
				t.Helper()
				s, e := store.New(ctx, backend, model, namespace, schema.Multichain)
				if e != nil {
					t.Fatal(e)
				}
				return s
			}
			s := newStore()
			root := "0x1000000000000000000000000000000000000000"
			childA := "0x2000000000000000000000000000000000000000"
			childB := "0x3000000000000000000000000000000000000000"
			created := abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address"}}}
			selector := abi.EventSelector(created)
			transferSelector := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
			var mu sync.Mutex
			head := uint64(1)
			fork := false
			hash := func(n uint64, alternate bool) string {
				if n == 0 {
					return fmt.Sprintf("0x%064x", 100)
				}
				if alternate {
					return fmt.Sprintf("0x%064x", 200+n)
				}
				return fmt.Sprintf("0x%064x", 100+n)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     any
					Method string
					Params []json.RawMessage
				}
				if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
					http.Error(w, e.Error(), 400)
					return
				}
				mu.Lock()
				tip, alternate := head, fork
				mu.Unlock()
				var result any
				switch req.Method {
				case "eth_chainId":
					result = "0x1"
				case "eth_getBlockByNumber":
					var requested string
					json.Unmarshal(req.Params[0], &requested)
					n := tip
					if requested != "latest" {
						n, _ = Number(requested)
					}
					result = Block{Number: Hex(n), Hash: hash(n, alternate), ParentHash: hash(n-1, alternate), Timestamp: Hex(1000 + n), Transactions: []map[string]any{{"hash": fmt.Sprintf("tx%d", n)}}}
				case "eth_getLogs":
					var query map[string]any
					json.Unmarshal(req.Params[0], &query)
					n := uint64(0)
					for i := uint64(0); i <= tip; i++ {
						if query["blockHash"] == hash(i, alternate) {
							n = i
							break
						}
					}
					child := childA
					if alternate {
						child = childB
					}
					logs := []Log{}
					if query["topics"] != nil {
						if n == 1 {
							logs = append(logs, Log{Address: root, Topics: []string{selector}, Data: "0x" + strings.Repeat("0", 24) + child[2:], BlockHash: hash(n, alternate), LogIndex: "0x0"})
						}
					} else {
						matched := false
						if addresses, ok := query["address"].([]any); ok {
							for _, a := range addresses {
								matched = matched || a == child
							}
						}
						if matched && n > 0 {
							amount := 5
							if n == 2 {
								amount = 7
							}
							if alternate {
								amount = 3
							}
							logs = append(logs, Log{Address: child, Topics: []string{transferSelector, "0x" + strings.Repeat("0", 64), "0x" + strings.Repeat("0", 24) + child[2:]}, Data: fmt.Sprintf("0x%064x", amount), BlockHash: hash(n, alternate), TransactionHash: fmt.Sprintf("tx%d", n), TransactionIndex: "0x0", LogIndex: "0x1"})
						}
					}
					result = logs
				default:
					http.Error(w, "unexpected RPC "+req.Method, 500)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			}))
			defer server.Close()
			parameter := "child"
			cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{server.URL}}}, Contracts: []config.Contract{{Name: "Token", Chain: "local", ABI: []byte(transferABI), StartBlock: 0, Factory: &factory.Config{Address: root, Event: created, Parameter: &parameter}}}}
			fail := true
			handlers := map[string]Handler{"Token:Transfer": func(ctx context.Context, c *Context) error {
				args := c.Event.Args.(map[string]any)
				id := c.Event.Log.Address
				row, e := c.DB.Find(ctx, "account", store.Row{"id": id})
				if e != nil {
					return e
				}
				balance := new(big.Int)
				if row != nil {
					balance.Set(row["balance"].(*big.Int))
				}
				balance.Add(balance, args["value"].(*big.Int))
				if e = c.DB.Write(ctx, "account", store.Row{"id": id, "balance": balance}, store.Upsert); e != nil {
					return e
				}
				if fail {
					return fmt.Errorf("injected handler failure after business write")
				}
				return nil
			}}
			newEngine := func() *Engine {
				t.Helper()
				e, err := New(cfg, s, handlers)
				if err != nil {
					t.Fatal(err)
				}
				e.Client.RequestsPerSecond = 0
				e.Client.Retries = 0
				return e
			}
			engine := newEngine()
			f := engine.factories["Token"]
			if e := engine.Sync(ctx); e == nil || !strings.Contains(e.Error(), "injected handler failure") {
				t.Fatalf("handler failure missing: %v", e)
			}
			rows, e := s.Read(ctx, "account")
			if e != nil || len(rows.Rows) != 0 {
				t.Fatalf("failed business write leaked: %v %v", rows, e)
			}
			children, e := s.FactoryChildren(ctx, f.ID, 1, 1)
			if e != nil || len(children) != 1 || children[childA] != 1 {
				t.Fatalf("sync provenance missing: %v %v", children, e)
			}
			// Close and reopen the actual PG connection/PGlite files, then resume.
			if e = backend.Close(); e != nil {
				t.Fatal(e)
			}
			backend = open()
			s = newStore()
			fail = false
			engine = newEngine()
			if e = engine.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			check := func(id, want string) {
				t.Helper()
				rows, e := s.Read(ctx, "account")
				if e != nil || len(rows.Rows) != 1 || rows.Rows[0]["id"] != id || fmt.Sprint(rows.Rows[0]["balance"]) != want {
					t.Fatalf("rows %v; want %s=%s; err %v", rows, id, want, e)
				}
			}
			check(childA, "5")
			mu.Lock()
			head = 2
			mu.Unlock()
			if e = engine.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			check(childA, "12")
			if e = engine.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			check(childA, "12")
			// Fork at block zero: the orphan child's business writes and sync
			// discovery both disappear, and the new child is indexed in its birth block.
			mu.Lock()
			fork = true
			head = 1
			mu.Unlock()
			if e = engine.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			check(childB, "3")
			children, e = s.FactoryChildren(ctx, f.ID, 1, 2)
			if e != nil || len(children) != 1 || children[childB] != 1 {
				t.Fatalf("orphan factory address remained: %v %v", children, e)
			}
			before, e := s.FactoryChildren(ctx, f.ID, 1, 0)
			if e != nil || len(before) != 0 {
				t.Fatalf("address visible before discovery: %v %v", before, e)
			}
			if e = backend.Close(); e != nil {
				t.Fatal(e)
			}
			backend = open()
			s = newStore()
			engine = newEngine()
			if e = engine.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			check(childB, "3")
			// Simulate a handler commit whose acknowledgement is lost before the
			// block-complete marker. Restart onto a fork and recover the durable
			// orphan write using the header saved before that handler.
			mu.Lock()
			head = 2
			mu.Unlock()
			s.Backend = &lostCommitAckBackend{Backend: backend, armed: true}
			if e = engine.Sync(ctx); e == nil || !strings.Contains(e.Error(), "injected lost commit acknowledgement") {
				t.Fatalf("missing commit failure %v", e)
			}
			check(childB, "6")
			block, e := s.SyncedBlock(ctx, 1, 2)
			if e != nil || block == nil {
				t.Fatalf("committed handler lost its block provenance: %v %v", block, e)
			}
			if e = backend.Close(); e != nil {
				t.Fatal(e)
			}
			backend = open()
			s = newStore()
			engine = newEngine()
			mu.Lock()
			fork = false
			mu.Unlock()
			if e = engine.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			check(childA, "12")
		})
	}
}

type lostCommitAckBackend struct {
	store.Backend
	armed bool
}

func (b *lostCommitAckBackend) Begin(ctx context.Context) (store.Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &lostCommitAckTransaction{Transaction: tx, backend: b}, nil
}

type lostCommitAckTransaction struct {
	store.Transaction
	backend  *lostCommitAckBackend
	business bool
}

func (tx *lostCommitAckTransaction) Exec(ctx context.Context, sql string, args ...any) error {
	e := tx.Transaction.Exec(ctx, sql, args...)
	if e == nil && strings.Contains(sql, `."account" `) && (strings.HasPrefix(sql, "INSERT INTO ") || strings.HasPrefix(sql, "UPDATE ")) {
		tx.business = true
	}
	return e
}
func (tx *lostCommitAckTransaction) Commit(ctx context.Context) error {
	e := tx.Transaction.Commit(ctx)
	if e == nil && tx.business && tx.backend.armed {
		tx.backend.armed = false
		return fmt.Errorf("injected lost commit acknowledgement")
	}
	return e
}
