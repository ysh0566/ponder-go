package store

import (
	"encoding/json"
	"math/big"
	"strconv"
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
		case int64:
			out[i] = strconv.FormatInt(x, 10)
		case uint64:
			out[i] = strconv.FormatUint(x, 10)
		case int:
			out[i] = strconv.FormatInt(int64(x), 10)
		case uint:
			out[i] = strconv.FormatUint(uint64(x), 10)
		default:
			out[i] = v
		}
	}
	return out
}
