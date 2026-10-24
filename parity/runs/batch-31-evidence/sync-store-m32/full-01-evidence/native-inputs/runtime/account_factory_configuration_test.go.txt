package runtime

import (
	"encoding/json"
	"reflect"
	"testing"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/factory"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Account defaults are a new API; constructing them must preserve caller data
// and the existing contract factory identity used by durable discovery/cache.
func TestAccountFactoryConfiguration(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "inherited_bounds"
		if explicit {
			name = "explicit_earlier_factory_history"
		}
		t.Run(name, func(t *testing.T) {
			end, from, to := uint64(10), uint64(3), uint64(8)
			parameter := "child"
			original := factory.Config{Address: []string{"0xA000000000000000000000000000000000000001", "0xa000000000000000000000000000000000000001"}, Event: abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address"}}}, Parameter: &parameter}
			accountFactory := original
			if explicit {
				accountFactory.FromBlock = &from
				accountFactory.ToBlock = &to
			}
			cfg := config.Config{Chains: []config.Chain{{Name: "local", ID: 1, RPC: []string{"http://127.0.0.1:1"}}}, Contracts: []config.Contract{{Name: "Token", Chain: "local", ABI: []byte(transferABI), StartBlock: 5, EndBlock: &end, Factory: &original}}, Accounts: []config.AccountSource{{Name: "Wallet", Chain: "local", StartBlock: 5, EndBlock: &end, Factory: &accountFactory}}}
			before, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := NewWithOptions(cfg, &store.Store{Ordering: schema.Multichain}, nil, Options{})
			if err != nil {
				t.Fatal(err)
			}
			account := engine.factories["Wallet"]
			wantFrom, wantTo := uint64(5), uint64(10)
			if explicit {
				wantFrom, wantTo = 3, 8
			}
			if account.FromBlock == nil || *account.FromBlock != wantFrom || account.ToBlock == nil || *account.ToBlock != wantTo || account.ChainID != 1 || account.SourceID != "Wallet" {
				t.Fatal("effective account factory identity/bounds", account)
			}
			start := uint64(5)
			legacyConfig := original
			legacyConfig.ChainID, legacyConfig.SourceID, legacyConfig.FromBlock = 1, "Token", &start
			legacy, err := factory.Build(legacyConfig)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(engine.factories["Token"], legacy) || engine.factories["Token"].ToBlock != nil {
				t.Fatal("existing contract factory identity/default changed", engine.factories["Token"], legacy)
			}
			after, err := json.Marshal(cfg)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("factory normalization mutated caller configuration", err)
			}
		})
	}
}
