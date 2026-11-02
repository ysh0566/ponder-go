package historical

import (
	"ponder.local/ponder-go/indexing"
)

func runtimeRPCCacheEnabled(chain indexing.Object) bool {
	enabled, _ := chain["cacheRpcRequests"].(bool)
	return enabled
}
