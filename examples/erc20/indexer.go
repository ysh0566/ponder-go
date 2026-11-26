package main

import (
	"context"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/logging"
	"github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/store"
)

func newProjectIndexer(_ context.Context, db *store.Store, network networkConfig, chain config.Chain, logger *logging.Logger) (runtime.ApplicationIndexer, error) {
	cfg, handlers := project(network, logger)
	cfg.Chains = []config.Chain{chain}
	return runtime.NewIndexerWithOptions(cfg, db, handlers, runtime.Options{Logger: logger})
}
