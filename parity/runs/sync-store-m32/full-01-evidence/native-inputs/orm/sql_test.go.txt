package orm_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"ponder.local/ponder-go/orm/testdata/strict"
	"ponder.local/ponder-go/store"
)

func TestTypedEagerSQL(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			s := modelStore(t, backend(t, dialect), "strict")
			address := "0x0000000000000000000000000000000000000000"
			if e := s.Apply(ctx, indexed("typed-sql", 1), func(w *store.Writer) error {
				table := strict.Account.Bind(w.Indexing())
				if _, e := table.Insert(ctx, strict.NewAccountInsert(address, *big.NewInt(10))); e != nil {
					return e
				}
				found, e := table.SQL().Find(ctx, strict.NewAccountKey(address))
				if e != nil || found == nil || found.Balance.Cmp(big.NewInt(10)) != 0 {
					return fmt.Errorf("buffered insert not visible to eager SQL: %#v %v", found, e)
				}
				_, e = table.SQL().Insert(ctx, strict.NewAccountInsert(address, *big.NewInt(20)))
				var raw *store.RawSQLError
				if !errors.As(e, &raw) {
					return fmt.Errorf("typed SQL duplicate did not reach database: %v", e)
				}
				other := "0x0000000000000000000000000000000000000001"
				inserted, e := table.SQL().Insert(ctx, strict.NewAccountInsert(other, *big.NewInt(30)))
				if e != nil || inserted.Address != other || inserted.Balance.Cmp(big.NewInt(30)) != 0 {
					return fmt.Errorf("typed SQL result: %#v %v", inserted, e)
				}
				found, e = table.Find(ctx, strict.NewAccountKey(other))
				if e != nil || found == nil || found.Balance.Cmp(big.NewInt(30)) != 0 {
					return fmt.Errorf("eager SQL did not invalidate buffered cache: %#v %v", found, e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if e := s.Revert(ctx, indexed("", 0).Checkpoint); e != nil {
				t.Fatal(e)
			}
			rows, e := s.Read(ctx, "account")
			if e != nil || len(rows.Rows) != 0 {
				t.Fatalf("typed SQL bypassed trigger undo: %#v %v", rows, e)
			}
		})
	}
}
