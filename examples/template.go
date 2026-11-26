// Package examples contains the sources distributed by ponder init.
package examples

import "embed"

// ERC20 contains the runnable single-chain Go starter.
//
//go:embed erc20/main.go erc20/indexer.go erc20/network.go erc20/api.go erc20/handlers.go erc20/schema.json erc20/network.json erc20/go.mod.tmpl erc20/package.json erc20/_dot_gitignore
var ERC20 embed.FS
