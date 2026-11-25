package orm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/orm/testdata/containers"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestGeneratedContainerModels(t *testing.T) {
	raw, err := os.ReadFile("testdata/containers/model.json")
	if err != nil {
		t.Fatal(err)
	}
	var model schema.Schema
	if err = json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	source, err := orm.GenerateModels("containers", model)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := os.ReadFile("testdata/containers/models.go")
	if err != nil || !bytes.Equal(source, checked) {
		t.Fatal("fixture differs from generated model", err)
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db := modelStore(t, backend(t, dialect), "containers")
			data := []byte{0, 255}
			doc := json.RawMessage(`{"n":9007199254740993}`)
			numbers := []orm.Nullable[int64]{orm.Some(int64(7)), orm.Null[int64](), orm.Some(int64(0))}
			origin := [2]float64{1, 2}
			line := [3]float64{1, 0, 3}
			input := containers.NewPacketInsert("id", data, doc, numbers).WithOrigin(orm.Some(origin)).WithAxis(orm.Some(line))
			data[0] = 77
			doc[0] = '!'
			numbers[0] = orm.Some(int64(99))
			if err := db.Apply(ctx, indexed("write", 1), func(w *store.Writer) error {
				_, err := containers.Packet.Bind(w.Indexing()).Insert(ctx, input)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Apply(ctx, indexed("read", 2), func(w *store.Writer) error {
				r, err := containers.Packet.Bind(w.Indexing()).Find(ctx, containers.NewPacketKey("id"))
				if err != nil {
					return err
				}
				if r == nil || !bytes.Equal(r.Payload, []byte{0, 255}) || string(r.Document) != `{"n":9007199254740993}` || !reflect.DeepEqual(r.Numbers, []orm.Nullable[int64]{orm.Some(int64(7)), orm.Null[int64](), orm.Some(int64(0))}) || !r.Origin.Valid() || r.Origin.Value() != origin || !r.Axis.Valid() || r.Axis.Value() != line {
					return fmt.Errorf("invalid persisted row: %+v", r)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
