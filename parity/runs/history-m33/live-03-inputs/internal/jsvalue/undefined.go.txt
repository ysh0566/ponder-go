// Package jsvalue supplies source values shared across dependency boundaries.
package jsvalue

// UndefinedValue distinguishes a missing JavaScript property from null.
type UndefinedValue struct{}

var Undefined = UndefinedValue{}

func (UndefinedValue) MarshalJSON() ([]byte, error) { return []byte(`{"$undefined":true}`), nil }
func IsUndefined(value any) bool                    { _, ok := value.(UndefinedValue); return ok }
