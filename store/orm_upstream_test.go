package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
	"reflect"
	"testing"
	"time"
)

func sourceAccountModel() schema.Schema {
	return schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
}

// The source uses userQB.transaction directly, without the runtime's event-end
// auto-flush. Keep that fixture boundary: buffered rows persist only where the
// original callback explicitly flushes. Some source cases install no SQL tables.
func sourceORMTransaction(t *testing.T, dialect string, model schema.Schema, physical bool, fn func(context.Context, *IndexingStore) error) {
	t.Helper()
	ctx := context.Background()
	b := sourceBackend(t, ctx, dialect)
	s, e := New(ctx, b, model, fmt.Sprintf("ponder_go_orm_%x", time.Now().UnixNano()), schema.Multichain)
	if e != nil {
		t.Fatal(e)
	}
	if !physical {
		for _, table := range model.Tables {
			if e = b.Exec(ctx, "DROP TABLE "+s.table(table.Name)+" CASCADE"); e != nil {
				t.Fatal(e)
			}
		}
	}
	tx, e := b.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	fixtureEvent := event("source-transaction", 1, 1)
	encoded := checkpoint.MustEncode(fixtureEvent.Checkpoint)
	if e = s.eventContext(ctx, tx, encoded, 1, false); e != nil {
		t.Fatal(e)
	}
	w := &Writer{s: s, tx: tx, event: fixtureEvent, encoded: encoded, changed: map[string]bool{}}
	defer w.closed.Store(true)
	if e = fn(ctx, w.Indexing(CacheOptions{Complete: true})); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
}

func expectSourceBalance(t *testing.T, row Row, want int64) {
	t.Helper()
	value, ok := row["balance"].(*big.Int)
	if !ok || value.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("source balance assertion: got %v; want %dn", row, want)
	}
}

func sourceValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case *big.Int:
		return map[string]any{"$bigint": v.String()}
	case *time.Time:
		return map[string]any{"$date": v.UnixMilli()}
	case time.Time:
		return map[string]any{"$date": v.UnixMilli()}
	case []byte:
		return map[string]any{"$bytes": hex.EncodeToString(v)}
	case Row:
		out := map[string]any{}
		for k, v := range v {
			out[k] = sourceValue(v)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, v := range v {
			out[k] = sourceValue(v)
		}
		return out
	case json.Number:
		return map[string]any{"$number": v.String()}
	}
	r := reflect.ValueOf(value)
	switch r.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, r.Len())
		for i := range out {
			out[i] = sourceValue(r.Index(i).Interface())
		}
		return out
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"$number": fmt.Sprint(r.Int())}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"$number": fmt.Sprint(r.Float())}
	}
	return value
}
func expectSourceMatch(t *testing.T, got Row, want Row) {
	t.Helper()
	actual := sourceValue(got).(map[string]any)
	expected := sourceValue(want).(map[string]any)
	for key, value := range expected {
		if !reflect.DeepEqual(actual[key], value) {
			t.Fatalf("source toMatchObject %s: got %#v; want %#v", key, actual[key], value)
		}
	}
}

func TestUpstreamORM(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Run("find", func(t *testing.T) {
				sourceORMTransaction(t, dialect, sourceAccountModel(), true, func(ctx context.Context, db *IndexingStore) error {
					key := Row{"address": sourceZeroAddress}
					result, e := db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					if result != nil {
						t.Fatal("source toBe(null)", result)
					}
					insertResult, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{})
					if e != nil {
						return e
					}
					result, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					if len(result) != 2 || result["address"] != sourceZeroAddress {
						t.Fatal("source toMatchObject address", result)
					}
					expectSourceBalance(t, result, 10)
					upsertResult, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(20)}, Conflict{Action: ConflictUpdate, Set: Row{"balance": big.NewInt(20)}})
					if e != nil {
						return e
					}
					expectSourceBalance(t, insertResult, 10)
					expectSourceBalance(t, result, 10)
					result, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					updateResult, e := db.Update(ctx, "account", key, Row{"balance": big.NewInt(30)})
					if e != nil {
						return e
					}
					expectSourceBalance(t, upsertResult, 20)
					expectSourceBalance(t, result, 20)
					if _, e = db.Update(ctx, "account", key, Row{"balance": big.NewInt(40)}); e != nil {
						return e
					}
					expectSourceBalance(t, updateResult, 30)
					db.Cache.Clear()
					db.Cache.Invalidate()
					result, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					if result != nil {
						t.Fatal("source clear/invalidate must expose unflushed DB", result)
					}
					return nil
				})
			})
			t.Run("insert keeps writes to a found row", func(t *testing.T) {
				model := sourceAccountModel()
				snapshot := model.Tables[0]
				snapshot.Name = "snapshot"
				model.Tables = append(model.Tables, snapshot)
				sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
					key := Row{"address": sourceZeroAddress}
					if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
						return e
					}
					row, e := db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					row["balance"] = big.NewInt(20)
					if _, e = db.InsertOne(ctx, "snapshot", row, Conflict{}); e != nil {
						return e
					}
					row, e = db.Find(ctx, "snapshot", key)
					if e != nil {
						return e
					}
					expectSourceBalance(t, row, 20)
					row, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					expectSourceBalance(t, row, 10)
					if e = db.Cache.Flush(ctx); e != nil {
						return e
					}
					db.Cache.Clear()
					db.Cache.Invalidate()
					row, e = db.Find(ctx, "snapshot", key)
					if e != nil {
						return e
					}
					expectSourceBalance(t, row, 20)
					row, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					expectSourceBalance(t, row, 10)
					return nil
				})
			})
			t.Run("insert", func(t *testing.T) {
				sourceORMTransaction(t, dialect, sourceAccountModel(), false, func(ctx context.Context, db *IndexingStore) error {
					address := func(n int) string { return fmt.Sprintf("0x%040x", n) }
					input := func(n int, balance int64) Row { return Row{"address": address(n), "balance": big.NewInt(balance)} }
					check := func(row Row, n int, balance int64) {
						t.Helper()
						if row["address"] != address(n) {
							t.Fatal("source address assertion", row)
						}
						expectSourceBalance(t, row, balance)
					}
					result, e := db.InsertOne(ctx, "account", input(0, 10), Conflict{})
					if e != nil {
						return e
					}
					check(result, 0, 10)
					result, e = db.Find(ctx, "account", Row{"address": address(0)})
					if e != nil {
						return e
					}
					check(result, 0, 10)
					results, e := db.InsertMany(ctx, "account", []Row{input(1, 12), input(2, 52)}, Conflict{})
					if e != nil {
						return e
					}
					if len(results) != 2 {
						t.Fatal(results)
					}
					check(results[0], 1, 12)
					check(results[1], 2, 52)
					result, e = db.Find(ctx, "account", Row{"address": address(1)})
					if e != nil {
						return e
					}
					check(result, 1, 12)
					result, e = db.Find(ctx, "account", Row{"address": address(2)})
					if e != nil {
						return e
					}
					check(result, 2, 52)
					result, e = db.InsertOne(ctx, "account", input(1, 44), Conflict{Action: ConflictNothing})
					if e != nil {
						return e
					}
					if result != nil {
						t.Fatal("source onConflictDoNothing null", result)
					}
					result, e = db.Find(ctx, "account", Row{"address": address(1)})
					if e != nil {
						return e
					}
					check(result, 1, 12)
					results, e = db.InsertMany(ctx, "account", []Row{input(1, 44), input(3, 0)}, Conflict{Action: ConflictNothing})
					if e != nil {
						return e
					}
					if len(results) != 2 || results[0] != nil {
						t.Fatal("source batch null slot", results)
					}
					check(results[1], 3, 0)
					result, e = db.Find(ctx, "account", Row{"address": address(1)})
					if e != nil {
						return e
					}
					check(result, 1, 12)
					singleUpsert, e := db.InsertOne(ctx, "account", input(1, 90), Conflict{Action: ConflictUpdate, Set: Row{"balance": big.NewInt(16)}})
					if e != nil {
						return e
					}
					result, e = db.Find(ctx, "account", Row{"address": address(1)})
					if e != nil {
						return e
					}
					check(result, 1, 16)
					_, e = db.InsertMany(ctx, "account", []Row{input(1, 44), input(2, 0)}, Conflict{Action: ConflictUpdate, Update: func(row Row) (Row, error) {
						return Row{"balance": new(big.Int).Add(row["balance"].(*big.Int), big.NewInt(16))}, nil
					}})
					if e != nil {
						return e
					}
					expectSourceBalance(t, singleUpsert, 16)
					result, e = db.Find(ctx, "account", Row{"address": address(1)})
					if e != nil {
						return e
					}
					check(result, 1, 32)
					return nil
				})
			})
			t.Run("update", func(t *testing.T) {
				sourceORMTransaction(t, dialect, sourceAccountModel(), false, func(ctx context.Context, db *IndexingStore) error {
					key := Row{"address": sourceZeroAddress}
					if _, e := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": big.NewInt(10)}, Conflict{}); e != nil {
						return e
					}
					result, e := db.Update(ctx, "account", key, Row{"balance": big.NewInt(12)})
					if e != nil {
						return e
					}
					if result["address"] != sourceZeroAddress {
						t.Fatal(result)
					}
					expectSourceBalance(t, result, 12)
					result, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					if result["address"] != sourceZeroAddress {
						t.Fatal(result)
					}
					expectSourceBalance(t, result, 12)
					result, e = db.UpdateFunc(ctx, "account", key, func(row Row) (Row, error) {
						return Row{"balance": new(big.Int).Add(row["balance"].(*big.Int), big.NewInt(10))}, nil
					})
					if e != nil {
						return e
					}
					if result["address"] != sourceZeroAddress {
						t.Fatal(result)
					}
					expectSourceBalance(t, result, 22)
					result, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					if result["address"] != sourceZeroAddress {
						t.Fatal(result)
					}
					expectSourceBalance(t, result, 22)
					result, e = db.Update(ctx, "account", key, Row{"balance": Undefined})
					if e != nil {
						return e
					}
					if result["address"] != sourceZeroAddress {
						t.Fatal(result)
					}
					expectSourceBalance(t, result, 22)
					result, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					if result["address"] != sourceZeroAddress {
						t.Fatal(result)
					}
					expectSourceBalance(t, result, 22)
					return nil
				})
			})
			t.Run("update mutable values", func(t *testing.T) {
				model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "metadata", Type: "json", NotNull: true}, {Name: "values", Type: "integer[]", NotNull: true}, {Name: "calldata", Type: "bytes", NotNull: true}, {Name: "timestamp", Type: "timestamp", NotNull: true}}}}}
				sourceORMTransaction(t, dialect, model, true, func(ctx context.Context, db *IndexingStore) error {
					date := func(ms int64) *time.Time { v := time.UnixMilli(ms).UTC(); return &v }
					key := Row{"address": sourceZeroAddress}
					value := func(count int64, nested string, values []int64, bytes []byte, ms int64) Row {
						return Row{"metadata": map[string]any{"count": count, "nested": map[string]any{"value": nested}}, "values": values, "calldata": bytes, "timestamp": date(ms)}
					}
					initial := value(1, "one", []int64{1}, []byte{1}, 1000)
					initial["address"] = sourceZeroAddress
					if _, e := db.InsertOne(ctx, "account", initial, Conflict{}); e != nil {
						return e
					}
					previous, e := db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					metadata := map[string]any{"count": int64(2), "nested": map[string]any{"value": "two"}}
					values := []int64{2}
					calldata := []byte{2}
					timestamp := date(2000)
					set := Row{"metadata": metadata, "values": values, "calldata": calldata, "timestamp": timestamp}
					input := Row{"address": sourceZeroAddress, "metadata": metadata, "values": values, "calldata": calldata, "timestamp": timestamp}
					upserted, e := db.InsertOne(ctx, "account", input, Conflict{Action: ConflictUpdate, Set: set})
					if e != nil {
						return e
					}
					metadata["nested"].(map[string]any)["value"] = "mutated input"
					values = append(values, 9)
					calldata[0] = 9
					*timestamp = *date(9000)
					upserted["metadata"].(map[string]any)["nested"].(map[string]any)["value"] = "mutated result"
					upserted["values"] = append(upserted["values"].([]int64), 9)
					upserted["calldata"].([]byte)[0] = 9
					*upserted["timestamp"].(*time.Time) = *date(9000)
					expectSourceMatch(t, previous, value(1, "one", []int64{1}, []byte{1}, 1000))
					found, e := db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					expectSourceMatch(t, found, value(2, "two", []int64{2}, []byte{2}, 2000))
					updated, e := db.UpdateFunc(ctx, "account", key, func(row Row) (Row, error) {
						m := row["metadata"].(map[string]any)
						v := append(row["values"].([]int64), 3)
						return value(int64(m["count"].(float64))+1, "three", v, []byte{3}, 3000), nil
					})
					if e != nil {
						return e
					}
					updated["metadata"].(map[string]any)["nested"].(map[string]any)["value"] = "mutated update result"
					updated["values"] = append(updated["values"].([]int64), 9)
					updated["calldata"].([]byte)[0] = 9
					*updated["timestamp"].(*time.Time) = *date(9000)
					if e = db.Cache.Flush(ctx); e != nil {
						return e
					}
					db.Cache.Clear()
					db.Cache.Invalidate()
					found, e = db.Find(ctx, "account", key)
					if e != nil {
						return e
					}
					expectSourceMatch(t, found, value(3, "three", []int64{2, 3}, []byte{3}, 3000))
					return nil
				})
			})
		})
	}
}
