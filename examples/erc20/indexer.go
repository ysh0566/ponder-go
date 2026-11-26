package main

import (
	"context"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/logging"
	"github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/store"
)

func newProjectIndexer(_ context.Context, db *store.Store, networks []networkConfig, logger *logging.Logger) (runtime.ApplicationIndexer, error) {
	network := networks[0]
	cfg, handlers := project(network, logger)
	cfg.Chains = []config.Chain{network.chain()}
	return runtime.NewIndexerWithOptions(cfg, db, handlers, runtime.Options{Logger: logger})
}
