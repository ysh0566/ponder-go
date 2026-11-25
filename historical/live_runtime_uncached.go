package historical

import (
	"github.com/ysh0566/ponder-go/indexing"
)

func runtimeRPCCacheEnabled(chain indexing.Object) bool {
	enabled, _ := chain["cacheRpcRequests"].(bool)
	return enabled
}
