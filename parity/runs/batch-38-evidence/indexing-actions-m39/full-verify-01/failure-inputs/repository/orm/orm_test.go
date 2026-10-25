package orm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/orm"
	"ponder.local/ponder-go/orm/testdata/nullable"
	"ponder.local/ponder-go/orm/testdata/strict"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

func backend(t *testing.T, kind string) store.Backend {
	t.Helper()
	ctx := context.Background()
	var b store.Backend
	var e error
	if kind == "postgres" {
		url := os.Getenv("PONDER_TEST_POSTGRES")
		if url == "" {
			t.Skip("explicit task database required; no parity credit")
		}
		b, e = store.OpenPostgres(ctx, url)
	} else {
		b, e = store.OpenPGlite(ctx, "memory://")
	}
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	return b
}
func modelStore(t *testing.T, b store.Backend, fixture string) *store.Store {
	t.Helper()
	raw, e := os.ReadFile("testdata/" + fixture + "/model.json")
	if e != nil {
		t.Fatal(e)
	}
	var model schema.Schema
	if e = json.Unmarshal(raw, &model); e != nil {
		t.Fatal(e)
	}
	s, e := store.New(context.Background(), b, model, fmt.Sprintf("ponder_go_typed_%x", time.Now().UnixNano()), schema.Multichain)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func indexed(id string, block uint64) store.Event {
	return store.Event{ID: id, Checkpoint: checkpoint.Checkpoint{ChainID: 1, BlockNumber: block}}
}

func TestTypedJSON(t *testing.T) {
	row := strict.AccountRow{Address: "0x01", Balance: *big.NewInt(10)}
	raw, e := json.Marshal(row)
	if e != nil || string(raw) != `{"address":"0x01","balance":10}` {
		t.Fatalf("typed bigint JSON: %s %v", raw, e)
	}
	for _, valid := range []bool{false, true} {
		balance := orm.Null[big.Int]()
		want := `{"address":"0x01","balance":null}`
		if valid {
			balance = orm.Some(*big.NewInt(0))
			want = `{"address":"0x01","balance":0}`
		}
		value := nullable.AccountRow{Address: "0x01", Balance: balance}
		raw, e = json.Marshal(value)
		if e != nil || string(raw) != want {
			t.Fatalf("typed nullable JSON: %s %v", raw, e)
		}
		var roundtrip nullable.AccountRow
		if e = json.Unmarshal(raw, &roundtrip); e != nil {
			t.Fatal(e)
		}
		if roundtrip.Address != "0x01" || roundtrip.Balance.Valid() != valid {
			t.Fatalf("typed JSON roundtrip %v", roundtrip)
		}
		if valid {
			zero := roundtrip.Balance.Value()
			if zero.Sign() != 0 {
				t.Fatal(zero.String())
			}
		}
	}
}

func TestTypedRuntime(t *testing.T) {
	for _, kind := range []string{"pglite", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s := modelStore(t, backend(t, kind), "strict")
			address := "0x0000000000000000000000000000000000000000"
			key := strict.NewAccountKey(address)
			if e := s.Apply(ctx, indexed("insert", 1), func(w *store.Writer) error {
				table := strict.Account.Bind(w.Indexing())
				balance := *big.NewInt(10)
				input := strict.NewAccountInsert(address, balance)
				balance.SetInt64(90)
				row, e := table.Insert(ctx, input)
				if e != nil {
					return e
				}
				if row.Address != address || row.Balance.Cmp(big.NewInt(10)) != 0 {
					return fmt.Errorf("typed insert %v", row)
				}
				row.Balance.SetInt64(88)
				found, e := table.Find(ctx, key)
				if e != nil {
					return e
				}
				if found == nil || found.Balance.Cmp(big.NewInt(10)) != 0 {
					return fmt.Errorf("typed mutable copy %v", found)
				}
				ignored, e := table.InsertOrIgnore(ctx, strict.NewAccountInsert(address, *big.NewInt(20)))
				if e != nil {
					return e
				}
				if ignored != nil {
					return fmt.Errorf("typed ignore conflict %v", ignored)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e := s.Apply(ctx, indexed("update", 2), func(w *store.Writer) error {
				table := strict.Account.Bind(w.Indexing())
				row, e := table.Update(ctx, key, strict.AccountUpdate{Balance: orm.Set(*big.NewInt(20))})
				if e != nil {
					return e
				}
				if row.Balance.Cmp(big.NewInt(20)) != 0 {
					return fmt.Errorf("typed update %v", row)
				}
				row, e = table.Upsert(ctx, strict.NewAccountInsert(address, *big.NewInt(90)), strict.AccountUpdate{Balance: orm.Set(*big.NewInt(30))})
				if e != nil {
					return e
				}
				if row.Balance.Cmp(big.NewInt(30)) != 0 {
					return fmt.Errorf("typed upsert %v", row)
				}
				row, e = table.UpdateFunc(ctx, key, func(row strict.AccountRow) (strict.AccountUpdate, error) {
					row.Balance.Add(&row.Balance, big.NewInt(10))
					return strict.AccountUpdate{Balance: orm.Set(row.Balance)}, nil
				})
				if e != nil {
					return e
				}
				if row.Balance.Cmp(big.NewInt(40)) != 0 {
					return fmt.Errorf("typed functional update %v", row)
				}
				rows, e := table.Select(ctx)
				if e != nil {
					return e
				}
				if len(rows) != 1 || rows[0].Balance.Cmp(big.NewInt(40)) != 0 {
					return fmt.Errorf("typed select %v", rows)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e := s.Revert(ctx, indexed("", 1).Checkpoint); e != nil {
				t.Fatal(e)
			}
			if e := s.Apply(ctx, indexed("delete", 2), func(w *store.Writer) error {
				table := strict.Account.Bind(w.Indexing())
				row, e := table.Find(ctx, key)
				if e != nil {
					return e
				}
				if row == nil || row.Balance.Cmp(big.NewInt(10)) != 0 {
					return fmt.Errorf("typed undo %v", row)
				}
				deleted, e := table.Delete(ctx, key)
				if e != nil {
					return e
				}
				if !deleted {
					return fmt.Errorf("typed delete missed")
				}
				row, e = table.Find(ctx, key)
				if e != nil {
					return e
				}
				if row != nil {
					return fmt.Errorf("typed deleted row visible")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestTypedNullable(t *testing.T) {
	for _, kind := range []string{"pglite", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s := modelStore(t, backend(t, kind), "nullable")
			address := "0x0000000000000000000000000000000000000000"
			if e := s.Apply(ctx, indexed("nullable", 1), func(w *store.Writer) error {
				table := nullable.Account.Bind(w.Indexing())
				row, e := table.Insert(ctx, nullable.NewAccountInsert(address))
				if e != nil {
					return e
				}
				if row.Balance.Valid() {
					return fmt.Errorf("omitted nullable is not SQL NULL")
				}
				key := nullable.NewAccountKey(address)
				row, e = table.Update(ctx, key, nullable.AccountUpdate{Balance: orm.Set(orm.Some(*big.NewInt(0)))})
				if e != nil {
					return e
				}
				zero := row.Balance.Value()
				if !row.Balance.Valid() || zero.Sign() != 0 {
					return fmt.Errorf("zero became NULL")
				}
				row, e = table.Update(ctx, key, nullable.AccountUpdate{})
				if e != nil {
					return e
				}
				if !row.Balance.Valid() {
					return fmt.Errorf("unchanged patch became NULL")
				}
				row, e = table.Update(ctx, key, nullable.AccountUpdate{Balance: orm.Set(orm.Null[big.Int]())})
				if e != nil {
					return e
				}
				if row.Balance.Valid() {
					return fmt.Errorf("explicit SQL NULL lost")
				}
				input := nullable.NewAccountInsert("0x01")
				modified := input.WithBalance(orm.Some(*big.NewInt(7)))
				base, e := table.Insert(ctx, input)
				if e != nil {
					return e
				}
				if base.Balance.Valid() {
					return fmt.Errorf("WithBalance mutated the original builder")
				}
				if _, e = table.Delete(ctx, nullable.NewAccountKey("0x01")); e != nil {
					return e
				}
				base, e = table.Insert(ctx, modified)
				if e != nil {
					return e
				}
				seven := base.Balance.Value()
				if !base.Balance.Valid() || seven.Cmp(big.NewInt(7)) != 0 {
					return fmt.Errorf("modified optional input %v", base)
				}
				rows, e := table.InsertMany(ctx, []nullable.AccountInsert{nullable.NewAccountInsert("0x02"), nullable.NewAccountInsert("0x03").WithBalance(orm.Some(*big.NewInt(0)))})
				if e != nil {
					return e
				}
				if len(rows) != 2 || rows[0].Balance.Valid() || !rows[1].Balance.Valid() {
					return fmt.Errorf("nullable typed batch %v", rows)
				}
				if e = w.Indexing().Cache.Flush(ctx); e != nil {
					return e
				}
				w.Indexing().Cache.Clear()
				w.Indexing().Cache.Invalidate()
				loaded, e := table.Find(ctx, key)
				if e != nil {
					return e
				}
				if loaded == nil || loaded.Balance.Valid() {
					return fmt.Errorf("NULL after SQL roundtrip %v", loaded)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
