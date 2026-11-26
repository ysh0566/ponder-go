package main

import (
	"cmp"
	"encoding/json"
	"os"

	"github.com/ysh0566/ponder-go/schema"
)

const projectOrdering = schema.Multichain

func projectNetworks(raw []byte) ([]networkConfig, error) {
	var network networkConfig
	if err := json.Unmarshal(raw, &network); err != nil {
		return nil, err
	}
	network.Address = cmp.Or(os.Getenv("PONDER_LOCAL_CONTRACT"), network.Address)
	network.rpcURL = os.Getenv("PONDER_LOCAL_RPC_URL")
	return []networkConfig{network}, nil
}
