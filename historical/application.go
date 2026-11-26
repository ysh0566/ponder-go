package historical

import (
	"context"
	"fmt"

	"github.com/ysh0566/ponder-go/query"
)

// QueryServer serves the runner's committed business rows and named chain
// checkpoints. Close it after the HTTP application stops and before Close on
// the runner. Both readers and callbacks use the same business database.
func (r *RuntimeRunner) QueryServer(ctx context.Context) (*query.Server, error) {
	server, err := query.NewReader(ctx, r.domains.BusinessReader())
	if err != nil {
		return nil, err
	}
	for _, chain := range r.build.Chains {
		server.ChainNames[runtimeChainID(chain)] = fmt.Sprint(chain["name"])
	}
	return server, nil
}
