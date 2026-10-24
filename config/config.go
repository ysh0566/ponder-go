// Package config defines native Go configuration. TS source compatibility is a separate track.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/schema"
	"regexp"
)

type Chain struct {
	Name              string   `json:"name"`
	ID                uint64   `json:"id"`
	RPC               []string `json:"rpc"`
	BlockTimeMS       *float64 `json:"blockTime,omitempty"`
	ReorgWindow       *int     `json:"reorgWindow,omitempty"`
	PollingIntervalMS int      `json:"pollingInterval,omitempty"`
}

func (c Chain) Window() int {
	if c.ReorgWindow == nil {
		return 180
	}
	return *c.ReorgWindow
}

type Contract struct {
	Name                       string          `json:"name"`
	Chain                      string          `json:"chain"`
	ABI                        json.RawMessage `json:"abi"`
	Address                    []string        `json:"address"`
	Factory                    *factory.Config `json:"factory,omitempty"`
	StartBlock                 uint64          `json:"startBlock"`
	EndBlock                   *uint64         `json:"endBlock,omitempty"`
	IncludeTransactionReceipts bool            `json:"includeTransactionReceipts,omitempty"`
	IncludeCallTraces          bool            `json:"includeCallTraces,omitempty"`
}
type BlockSource struct {
	Name       string  `json:"name"`
	Chain      string  `json:"chain"`
	StartBlock uint64  `json:"startBlock"`
	EndBlock   *uint64 `json:"endBlock,omitempty"`
	Interval   uint64  `json:"interval"`
}

// AccountSource is a native single-chain static, wildcard or factory account source.
// Complete source configuration DSL compatibility remains separate.
type AccountSource struct {
	Name       string          `json:"name"`
	Chain      string          `json:"chain"`
	Address    []string        `json:"address"`
	StartBlock uint64          `json:"startBlock"`
	EndBlock   *uint64         `json:"endBlock,omitempty"`
	Factory    *factory.Config `json:"factory,omitempty"`
}
type Config struct {
	Ordering  schema.Ordering `json:"ordering"`
	Chains    []Chain         `json:"chains"`
	Contracts []Contract      `json:"contracts"`
	Blocks    []BlockSource   `json:"blocks"`
	Accounts  []AccountSource `json:"accounts,omitempty"`
}

var address = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

func (c Config) Validate() error {
	if c.Ordering != "" && c.Ordering != schema.Multichain && c.Ordering != schema.Omnichain && c.Ordering != schema.Isolated {
		return fmt.Errorf("invalid ordering %q", c.Ordering)
	}
	if len(c.Chains) == 0 {
		return fmt.Errorf("at least one chain is required")
	}
	names := map[string]bool{}
	ids := map[uint64]bool{}
	for _, chain := range c.Chains {
		if chain.Name == "" || names[chain.Name] || ids[chain.ID] {
			return fmt.Errorf("chain names and IDs must be unique")
		}
		names[chain.Name] = true
		ids[chain.ID] = true
		if chain.Window() < 0 || chain.Window() > 600 {
			return fmt.Errorf("Invalid 'reorgWindow' for chain '%s'. Expected an integer between 0 and 600 seconds, got %d.", chain.Name, chain.Window())
		}
		if len(chain.RPC) == 0 {
			return fmt.Errorf("chain %s needs an RPC endpoint", chain.Name)
		}
		for _, endpoint := range chain.RPC {
			u, e := url.Parse(endpoint)
			if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") {
				return fmt.Errorf("invalid RPC endpoint")
			}
		}
	}
	sources := map[string]bool{}
	for _, contract := range c.Contracts {
		if !names[contract.Chain] || contract.Name == "" || sources[contract.Name] {
			return fmt.Errorf("invalid contract source %q", contract.Name)
		}
		sources[contract.Name] = true
		if contract.EndBlock != nil && *contract.EndBlock < contract.StartBlock {
			return fmt.Errorf("endBlock precedes startBlock")
		}
		for _, a := range contract.Address {
			if !address.MatchString(a) {
				return fmt.Errorf("invalid contract address %q", a)
			}
		}
		if contract.Factory != nil {
			if len(contract.Address) > 0 {
				return fmt.Errorf("contract %s must choose static addresses or a factory", contract.Name)
			}
			if contract.Factory.Event.Name == "" || contract.Factory.Event.Type != "event" {
				return fmt.Errorf("factory for %s requires an ABI event", contract.Name)
			}
			if contract.Factory.FromBlock != nil && contract.Factory.ToBlock != nil && *contract.Factory.ToBlock < *contract.Factory.FromBlock {
				return fmt.Errorf("factory end block precedes start block")
			}
			if _, e := factory.Build(*contract.Factory); e != nil {
				return e
			}
		}
		if _, e := abi.Parse(string(contract.ABI)); e != nil {
			return e
		}
	}
	for _, account := range c.Accounts {
		if !names[account.Chain] || account.Name == "" || sources[account.Name] {
			return fmt.Errorf("invalid account source %q", account.Name)
		}
		sources[account.Name] = true
		if account.EndBlock != nil && *account.EndBlock < account.StartBlock {
			return fmt.Errorf("account end block precedes start")
		}
		if account.Factory != nil {
			if account.Address != nil {
				return fmt.Errorf("account %s must choose static addresses or a factory", account.Name)
			}
			if account.Factory.Event.Name == "" || account.Factory.Event.Type != "event" {
				return fmt.Errorf("factory for %s requires an ABI event", account.Name)
			}
			validateParent := func(value string) error {
				if !address.MatchString(value) {
					return fmt.Errorf("invalid account factory address %q", value)
				}
				return nil
			}
			switch parent := account.Factory.Address.(type) {
			case nil:
			case string:
				if err := validateParent(parent); err != nil {
					return err
				}
			case []string:
				if len(parent) == 0 {
					return fmt.Errorf("account factory parent array must not be empty")
				}
				for _, value := range parent {
					if err := validateParent(value); err != nil {
						return err
					}
				}
			case []any:
				if len(parent) == 0 {
					return fmt.Errorf("account factory parent array must not be empty")
				}
				for _, value := range parent {
					text, ok := value.(string)
					if !ok {
						return fmt.Errorf("account factory addresses must be strings")
					}
					if err := validateParent(text); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("account factory address must be a string or array")
			}
			from := account.StartBlock
			if account.Factory.FromBlock != nil {
				from = *account.Factory.FromBlock
			}
			if from > account.StartBlock {
				return fmt.Errorf("account start block precedes factory start block")
			}
			to := account.Factory.ToBlock
			if to == nil {
				to = account.EndBlock
			}
			if to != nil && *to < from {
				return fmt.Errorf("account factory end block precedes start block")
			}
			if account.EndBlock != nil && (to == nil || *to > *account.EndBlock) {
				return fmt.Errorf("account end block precedes factory end block")
			}
			if _, err := factory.Build(*account.Factory); err != nil {
				return err
			}
		}
		for _, value := range account.Address {
			if !address.MatchString(value) {
				return fmt.Errorf("invalid account address %q", value)
			}
		}
	}
	for _, block := range c.Blocks {
		if !names[block.Chain] || block.Name == "" || sources[block.Name] {
			return fmt.Errorf("invalid block source %q", block.Name)
		}
		sources[block.Name] = true
		if block.Interval == 0 {
			return fmt.Errorf("block interval must be positive")
		}
	}
	return nil
}
