package orm

import (
	"strings"
	"testing"

	"ponder.local/ponder-go/schema"
)

func TestGenerateModels(t *testing.T) {
	model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
	source, e := GenerateModels("models", model)
	if e != nil {
		t.Fatal(e)
	}
	for _, fragment := range []string{"type AccountKey struct", "func NewAccountInsert(v0 string, v1 big.Int)", "type AccountUpdate struct", "Balance orm.Change[big.Int]", "var Account = orm.Define[AccountKey, AccountInsert, AccountUpdate, AccountRow]"} {
		if !strings.Contains(string(source), fragment) {
			t.Fatalf("missing public typed API %q", fragment)
		}
	}
	update := string(source[strings.Index(string(source), "type AccountUpdate struct"):])
	update = update[:strings.Index(update, "}")]
	if strings.Contains(update, "Address") {
		t.Fatal("primary key exposed in update type")
	}
}
