package orm

import (
	"fmt"

	"github.com/ysh0566/ponder-go/store"
)

// ColumnCodec converts between a Go domain value and the native value used by
// its storage column. Generated models apply it at the typed table boundary.
type ColumnCodec[T any] struct {
	Encode func(T) (any, error)
	Decode func(any) (T, error)
}

// Read converts a non-NULL native column value to its application type.
func (c ColumnCodec[T]) Read(value any) (T, error) {
	if c.Decode == nil {
		var zero T
		return zero, fmt.Errorf("missing column decoder")
	}
	return c.Decode(value)
}

// EncodedColumn holds the result of encoding one immutable builder field.
// Keeping errors per field lets With methods replace an invalid optional value.
type EncodedColumn struct {
	value any
	err   error
}

func (c ColumnCodec[T]) encode(value T) (any, error) {
	if c.Encode == nil {
		return nil, fmt.Errorf("missing column encoder")
	}
	return c.Encode(value)
}

// Snapshot captures mutable data when a generated key or insert is built.
func (c ColumnCodec[T]) Snapshot(value T) EncodedColumn {
	raw, err := c.encode(value)
	if err != nil {
		return EncodedColumn{err: err}
	}
	return EncodedColumn{value: store.CopyValue(raw, false)}
}

// ResolveColumnValues checks every custom field before calling the executor.
func ResolveColumnValues(table string, row store.Row) (store.Row, error) {
	out := CopyValues(row)
	for name, value := range out {
		if field, ok := value.(EncodedColumn); ok {
			if field.err != nil {
				return nil, fmt.Errorf("%s.%s: %w", table, name, field.err)
			}
			out[name] = field.value
		}
	}
	return out, nil
}

// ArrayColumnCodec applies a scalar codec to each non-NULL PostgreSQL array
// element. Both the element and column can be independently nullable.
func ArrayColumnCodec[T any](element ColumnCodec[T]) ColumnCodec[[]Nullable[T]] {
	return ColumnCodec[[]Nullable[T]]{
		Decode: ArrayDecoder(element.Read),
		Encode: func(values []Nullable[T]) (any, error) {
			if values == nil {
				return nil, nil
			}
			out := make([]any, len(values))
			for i, value := range values {
				if value.Valid() {
					raw, err := element.encode(value.Value())
					if err != nil {
						return nil, fmt.Errorf("element %d: %w", i, err)
					}
					out[i] = raw
				}
			}
			return out, nil
		},
	}
}
