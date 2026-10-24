package sqlquery

import (
	"strings"
	"sync"
	"testing"
)

// Supplementary cache/API checks. They are not additional original mappings.
func TestSQLCacheBehavior(t *testing.T) {
	t.Run("cached rejection and recency", func(t *testing.T) {
		v := newValidator(2)
		first := "SET statement_timeout = '1s';"
		second := "SET statement_timeout = '2s';"
		for _, sql := range []string{first, second} {
			if err := v.Validate(sql); err == nil || err.Error() != "VariableSetStmt not supported" {
				t.Fatalf("First rejection: %v", err)
			}
		}
		if err := v.Validate(first); err == nil || err.Error() != "Invalid query" {
			t.Fatalf("Cached rejection: %v", err)
		}
		if err := v.Validate("SELECT 1"); err != nil {
			t.Fatal(err)
		}
		if err := v.Validate(second); err == nil || err.Error() != "VariableSetStmt not supported" {
			t.Fatalf("Least recent rejection should have been evicted: %v", err)
		}
	})
	t.Run("relation set identity mutation and eviction", func(t *testing.T) {
		v := newValidator(2)
		one, err := v.Relations("SELECT * FROM users")
		if err != nil {
			t.Fatal(err)
		}
		one.Add("clientAdded")
		again, err := v.Relations("SELECT * FROM users")
		if err != nil || again != one || !again.Has("clientAdded") {
			t.Fatalf("Shared set identity/mutation: %v", err)
		}
		snapshot := one.Values()
		snapshot[0] = "snapshotOnly"
		if one.Has("snapshotOnly") || !one.Has("users") {
			t.Fatal("Values must be an isolated iteration snapshot")
		}
		two, err := v.Relations("SELECT * FROM metadata")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = v.Relations("SELECT * FROM users"); err != nil {
			t.Fatal(err)
		}
		if _, err = v.Relations("SELECT * FROM account"); err != nil {
			t.Fatal(err)
		}
		fresh, err := v.Relations("SELECT * FROM metadata")
		if err != nil || fresh == two {
			t.Fatalf("Least recent relation set should have been evicted: %v", err)
		}
		if !one.Delete("clientAdded") || one.Has("clientAdded") || one.Delete("missing") {
			t.Fatal("Set deletion semantics")
		}
		one.Clear()
		if one.Len() != 0 {
			t.Fatal("Set clear semantics")
		}
	})
	t.Run("source UTF16 cap", func(t *testing.T) {
		// 2,000 supplementary code points occupy 4,000 JavaScript UTF-16
		// units but 8,000 UTF-8 bytes. The source accepts this actual SQL.
		sql := "SELECT 1 /*" + strings.Repeat("😀", 2_000) + "*/"
		v := NewValidator()
		if err := v.Validate(sql); err != nil {
			t.Fatal(err)
		}
		if _, err := v.Relations(sql); err != nil {
			t.Fatal(err)
		}
		long := "SELECT 1 /*" + strings.Repeat("😀", 2_500) + "*/"
		if _, err := Parse(long); err == nil || err.Error() != "Invalid query" {
			t.Fatalf("Public parser cap: %v", err)
		}
		if _, err := ParseStatement(long); err != nil {
			t.Fatalf("Trusted indexing parser retains its uncapped path: %v", err)
		}
	})
	t.Run("concurrent cached sets and validations", func(t *testing.T) {
		v := NewValidator()
		const sql = "SELECT col FROM users JOIN metadata ON users.id = metadata.id;"
		set, err := v.Relations(sql)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 16; j++ {
					if err := v.Validate(sql); err != nil {
						t.Error(err)
					}
					actual, err := v.Relations(sql)
					if err != nil || actual != set || !actual.Has("users") || actual.Len() != 2 {
						t.Errorf("Concurrent cache hit: %v", err)
					}
					_ = actual.Values()
				}
			}()
		}
		wg.Wait()
	})
}
