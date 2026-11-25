package store

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"reflect"
	"slices"
	"time"
)

// RowCopy borrows a row until a write or a read of a mutable value. Row values
// follow the indexing store's ownership rules for database and JSON data.
type RowCopy struct {
	row   Row
	owned bool
}

// CopyOnWrite creates a view of a plain row. The caller owns synchronization
// when sharing the source map. IndexingStore.FindCopy borrows immutable cache rows.
func CopyOnWrite(row Row) *RowCopy { return &RowCopy{row: row} }

func (v *RowCopy) detach() {
	if v.owned {
		return
	}
	v.row = cloneRow(v.row)
	if v.row == nil {
		v.row = Row{}
	}
	v.owned = true
}

func mutableRowValue(value any) bool {
	if value == nil {
		return false
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Array, reflect.Map, reflect.Pointer, reflect.Slice, reflect.Struct:
		return true
	}
	return false
}

func (v *RowCopy) Get(key string) (any, bool) {
	value, ok := v.row[key]
	if !v.owned && mutableRowValue(value) {
		v.detach()
		value = v.row[key]
	}
	return value, ok
}

func (v *RowCopy) Set(key string, value any) {
	v.detach()
	v.row[key] = value
}
func (v *RowCopy) Delete(key string) {
	v.detach()
	delete(v.row, key)
}

func (v *RowCopy) Has(key string) bool          { _, ok := v.row[key]; return ok }
func (v *RowCopy) Keys() []string               { return slices.Sorted(maps.Keys(v.row)) }
func (v *RowCopy) String() string               { return fmt.Sprint(v.row) }
func (v *RowCopy) MarshalJSON() ([]byte, error) { return json.Marshal(v.row) }

// Snapshot returns a plain, independently mutable row with all writes applied.
func (v *RowCopy) Snapshot() Row { return cloneRow(v.row) }

// CopyValue copies database values and materializes nested row views during a
// deep copy. A shallow copy owns its top-level container.
func CopyValue(value any, shallow bool) any {
	if view, ok := value.(*RowCopy); ok {
		if view == nil {
			return nil
		}
		if !shallow {
			return view.Snapshot()
		}
		// A shallow result may expose mutable fields. Detach them from a borrowed
		// cache row before the caller receives the container.
		if !view.owned {
			for _, x := range view.row {
				if mutableRowValue(x) {
					view.detach()
					break
				}
			}
		}
		return maps.Clone(view.row)
	}
	if !shallow {
		return cloneValue(value)
	}
	if value == nil {
		return nil
	}
	r := reflect.ValueOf(value)
	switch r.Kind() {
	case reflect.Map:
		if r.IsNil() {
			return value
		}
		out := reflect.MakeMapWithSize(r.Type(), r.Len())
		it := r.MapRange()
		for it.Next() {
			out.SetMapIndex(it.Key(), it.Value())
		}
		return out.Interface()
	case reflect.Slice:
		if r.IsNil() {
			return value
		}
		out := reflect.MakeSlice(r.Type(), r.Len(), r.Len())
		reflect.Copy(out, r)
		return out.Interface()
	default:
		return value
	}
}

// Identify containers before descending into them so repeated references and
// cycles inside a row point at the new storage. Overlapping slice views with
// different lengths have different visible elements.
type copyIdentity struct {
	typ     reflect.Type
	pointer uintptr
	length  int
}

type rowCloner map[copyIdentity]reflect.Value

func cloneRow(row Row) Row {
	if row == nil {
		return nil
	}
	return rowCloner{}.clone(row).(Row)
}

func cloneValue(value any) any { return rowCloner{}.clone(value) }

func (c rowCloner) clone(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case *RowCopy:
		if v == nil {
			return nil
		}
		return c.clone(v.row)
	case RowCopy:
		return c.clone(v.row)
	case []byte:
		return slices.Clone(v)
	case *big.Int:
		if v == nil {
			return nil
		}
		return new(big.Int).Set(v)
	case big.Int:
		return *new(big.Int).Set(&v)
	case *time.Time:
		if v == nil {
			return nil
		}
		t := *v
		return &t
	case time.Time:
		return v
	}
	r := reflect.ValueOf(value)
	var id copyIdentity
	switch r.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer:
		if r.IsNil() {
			return value
		}
		id = copyIdentity{typ: r.Type(), pointer: r.Pointer()}
		if r.Kind() == reflect.Slice {
			id.length = r.Len()
		}
		if previous, ok := c[id]; ok {
			return previous.Interface()
		}
	}
	switch r.Kind() {
	case reflect.Array, reflect.Slice:
		var out reflect.Value
		if r.Kind() == reflect.Slice {
			out = reflect.MakeSlice(r.Type(), r.Len(), r.Len())
			c[id] = out
		} else {
			out = reflect.New(r.Type()).Elem()
		}
		for i := 0; i < r.Len(); i++ {
			out.Index(i).Set(c.element(r.Index(i)))
		}
		return out.Interface()
	case reflect.Map:
		out := reflect.MakeMapWithSize(r.Type(), r.Len())
		c[id] = out
		it := r.MapRange()
		for it.Next() {
			out.SetMapIndex(it.Key(), c.element(it.Value()))
		}
		return out.Interface()
	case reflect.Pointer:
		out := reflect.New(r.Type().Elem()).Convert(r.Type())
		c[id] = out
		out.Elem().Set(c.element(r.Elem()))
		return out.Interface()
	case reflect.Struct:
		out := reflect.New(r.Type()).Elem()
		out.Set(r)
		for i := 0; i < r.NumField(); i++ {
			if r.Type().Field(i).IsExported() {
				out.Field(i).Set(c.element(r.Field(i)))
			}
		}
		return out.Interface()
	default:
		return value
	}
}

// Concrete Go containers retain their declared row-view element type. Values
// held in ordinary JSON containers materialize as plain rows.
func (c rowCloner) element(v reflect.Value) reflect.Value {
	cloned := c.clone(v.Interface())
	if cloned == nil {
		return reflect.Zero(v.Type())
	}
	out := reflect.ValueOf(cloned)
	if out.Type().AssignableTo(v.Type()) {
		return out
	}
	view := RowCopy{row: cloned.(Row), owned: true}
	if v.Type() == reflect.TypeFor[RowCopy]() {
		return reflect.ValueOf(view)
	}
	return reflect.ValueOf(&view)
}
