package store

import (
	"encoding/json"
	"math/big"
)

// Decimal integers travel as strings on both drivers, preserving uint256 values
// through the PGlite JSON bridge as well as PostgreSQL numeric parameters.
func databaseArgs(args []any) []any {
	out := make([]any, len(args))
	for i, v := range args {
		switch x := v.(type) {
		case *big.Int:
			if x == nil {
				out[i] = nil
			} else {
				out[i] = x.String()
			}
		case json.Number:
			out[i] = x.String()
		default:
			out[i] = v
		}
	}
	return out
}
