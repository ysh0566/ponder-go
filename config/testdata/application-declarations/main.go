package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"appfixture.test/events"
	"appfixture.test/market"
	"appfixture.test/weak"
	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/config"
)

// Named singleton values express the literal types the TypeScript caller infers.
type mainnetID struct{}

func (mainnetID) MarshalJSON() ([]byte, error) { return []byte("1"), nil }

type mainnet struct{}

func (mainnet) MarshalText() ([]byte, error) { return []byte("mainnet"), nil }

type hexPrefix struct{}

func (hexPrefix) MarshalText() ([]byte, error) { return []byte("0x"), nil }

type networks struct {
	Mainnet  config.ChainDeclaration[mainnetID, string] `json:"mainnet"`
	Optimism config.ChainDeclaration[int, string]       `json:"optimism"`
}
type arguments struct {
	Arg [1]hexPrefix `json:"arg"`
}
type eventFilter = config.EventFilterDeclaration[events.EventNameEvent0, arguments]
type contract = config.ContractDeclaration[events.ContractABI, mainnet, eventFilter]
type basic = config.ContractDeclaration[[1]events.EventItemEvent1, string, struct{}]
type contracts[T any] struct {
	C2 T `json:"c2"`
}
type pair[T any] struct {
	C1 basic `json:"c1"`
	C2 T     `json:"c2"`
}

func checked[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
func application[C, K, A any](chains C, contracts *K, accounts *A) *config.ApplicationDeclaration[C, K, A, struct{}] {
	def := config.ApplicationDeclaration[C, K, A, struct{}]{Chains: chains, Contracts: contracts, Accounts: accounts}
	conf := config.CreateConfig(&def)
	if conf != &def {
		panic("configuration identity changed")
	}
	source := checked(conf.Source())
	var wire map[string]any
	data := checked(json.Marshal(conf))
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(wire, source.Value()) {
		panic("configuration changed on conversion to native source")
	}
	return conf
}
func app[K any](n networks, k K) *config.ApplicationDeclaration[networks, K, struct{}, struct{}] {
	return application(n, &k, (*struct{})(nil))
}
func strict(n networks, f eventFilter) any {
	conf := app(n, contracts[contract]{contract{Filter: &f}})
	var _ mainnetID = conf.Chains.Mainnet.ID
	var _ string = conf.Chains.Mainnet.RPC
	var _ events.ContractABI = conf.Contracts.C2.ABI
	var _ mainnet = conf.Contracts.C2.Chain
	var _ events.EventNameEvent0 = conf.Contracts.C2.Filter.Event
	var _ [1]hexPrefix = conf.Contracts.C2.Filter.Args.Arg
	return conf
}
func original(name string) any {
	const rpc = "https://rpc.com"
	n := networks{config.ChainDeclaration[mainnetID, string]{RPC: rpc}, config.ChainDeclaration[int, string]{ID: 10, RPC: rpc}}
	e := events.EventItemEvent0{}
	f := config.DeclareEventFilter(e.CompactName(), arguments{})
	opts := config.FactoryOptions{Address: "0x"}
	b := basic{Chain: "mainnet", StartBlock: 0}
	switch name {
	case "createConfig basic":
		other := b
		other.Chain = "optimism"
		conf := app(n, pair[basic]{b, other})
		built := checked(config.BuildSourceConfig(checked(conf.Source()), config.SourceBuildOptions{}))
		if len(built.RPCClients()) != 2 {
			panic("missing configured chains")
		}
		return conf
	case "createConfig no extra properties":
		// The original suppresses both static errors. Explicit extensions let the
		// runtime keep both fields. The closed types are checked separately.
		type chainWithExtra struct {
			config.ChainDeclaration[int, string]
			A int `json:"a"`
		}
		type c2WithExtra struct {
			config.ContractDeclaration[[1]events.EventItemEvent0, string, struct{}]
			A int `json:"a"`
		}
		chains := map[string]chainWithExtra{"mainnet": {ChainDeclaration: config.ChainDeclaration[int, string]{ID: 1, RPC: rpc}}}
		k := contracts[c2WithExtra]{}
		k.C2.Chain = "mainnet"
		return application(chains, &k, (*struct{})(nil))
	case "createConfig address", "createConfig factory":
		c := basic{Chain: "mainnet", Address: "0x1"}
		if name == "createConfig factory" {
			c.Address = checked(config.FactoryByParameter(e, e.Parameters().Arg, opts))
		}
		return app(n, contracts[basic]{c})
	case "createConfig with filter", "createConfig strict return type":
		return strict(n, f)
	case "createConfig with multiple filters":
		other := config.DeclareEventFilter(events.EventNameEvent1{}, [0]string{})
		filters := [2]any{f, other}
		c := config.ContractDeclaration[events.ContractABI, mainnet, [2]any]{Filter: &filters}
		return app(n, contracts[config.ContractDeclaration[events.ContractABI, mainnet, [2]any]]{c})
	case "createConfig chain overrides":
		type chains struct {
			Optimism config.ContractOverrideDeclaration[eventFilter] `json:"optimism"`
		}
		type overridden = config.ContractDeclaration[events.ContractABI, chains, struct{}]
		c := overridden{Chain: chains{config.ContractOverrideDeclaration[eventFilter]{Address: "0x", Filter: &f}}, StartBlock: 0}
		return app(n, pair[overridden]{b, c})
	case "createConfig weak Abi":
		type dynamic = config.ContractDeclaration[[]weak.Declaration, string, config.EventFilterDeclaration[string, struct{}]]
		filter := config.DeclareEventFilter("event0", struct{}{})
		c := dynamic{ABI: []weak.Declaration{weak.EventItemEvent0{}, weak.FunctionItemFunc{}}, Chain: "mainnet", Filter: &filter}
		return app(n, contracts[dynamic]{c})
	case "createConfig accounts":
		type account = config.AccountDeclaration[mainnet, [1]hexPrefix]
		accounts := map[string]account{"me": {}}
		return application(n, (*struct{})(nil), &accounts)
	case "factory with weak event":
		return checked(config.FactoryByParameter(abi.Event{}, config.DynamicFactoryParameter("arg"), opts))
	case "factory at line 29":
		return checked(config.FactoryByParameter(e, e.Parameters().Arg, opts))
	case "factory at line 43":
		m := market.EventItemCreateMarket{}
		return checked(config.FactoryByParameter(m, m.Parameters().ArgsArg2, config.FactoryOptions{Address: "0xa"}))
	default:
		panic(fmt.Sprintf("unknown source case %q", name))
	}
}
func main() {
	if err := json.NewEncoder(os.Stdout).Encode(original(os.Args[1])); err != nil {
		panic(err)
	}
}
