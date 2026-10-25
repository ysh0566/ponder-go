package rpc

import (
	"context"
	"encoding/json"
)

// CacheLookupResult is the single settlement of an admitted persistent lookup.
// Value belongs to the receiver after settlement; misses have Hit=false.
type CacheLookupResult struct {
	Value json.RawMessage
	Hit   bool
	Err   error
}

// AsyncCacheReader is optional synchronous admission of an actual prepared read
// to an owned queue. Implementations validate and snapshot request identity before
// returning a buffered single-result future. Merely launching CacheGet in a
// goroutine, or acknowledging goroutine startup, does not satisfy this contract.
// SQL completion, visibility and commit ordering remain backend-specific.
type AsyncCacheReader interface {
	BeginCacheGet(context.Context, string) <-chan CacheLookupResult
}
