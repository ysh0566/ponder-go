package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/logging"
	indexer "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/store"
)

func project(network networkConfig, logger *logging.Logger) ([]config.Contract, map[string]indexer.Handler) {
	abi := `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]}]`
	contract := network.contract("Token", []byte(abi))
	contract.IncludeTransactionReceipts = true
	return []config.Contract{contract}, map[string]indexer.Handler{"Token:Transfer": func(ctx context.Context, c *indexer.Context) error {
		logger.Trace("Processing Token:Transfer", "chain_id", c.Chain.ID)
		args := c.Event.Args.(map[string]any)
		value := args["value"].(*big.Int)
		for _, leg := range []struct {
			address string
			sign    int
		}{{args["from"].(string), -1}, {args["to"].(string), 1}} {
			row, e := c.DB.Find(ctx, "account", store.Row{"id": leg.address})
			if e != nil {
				return e
			}
			balance := new(big.Int)
			if row != nil {
				balance.SetString(fmt.Sprint(row["balance"]), 10)
			}
			delta := new(big.Int).Mul(value, big.NewInt(int64(leg.sign)))
			balance.Add(balance, delta)
			if e = c.DB.Write(ctx, "account", store.Row{"id": leg.address, "balance": balance.String()}, store.Upsert); e != nil {
				return e
			}
		}
		return nil
	}}
}
