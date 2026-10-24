package config

import (
	"encoding/json"
	"reflect"
	"testing"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/factory"
)

// Native factory configuration boundaries; no original source-test credit.
func TestAccountFactoryValidation(t *testing.T) {
	addr := "0x1000000000000000000000000000000000000001"
	param := "child"
	from, to := uint64(5), uint64(4)
	valid := func() Config {
		return Config{Chains: []Chain{{Name: "local", ID: 1, RPC: []string{"http://127.0.0.1:1"}}}, Accounts: []AccountSource{{Name: "A", Chain: "local", Factory: &factory.Config{Address: addr, Event: abi.Event{Type: "event", Name: "Created", Inputs: []abi.Parameter{{Name: "child", Type: "address"}}}, Parameter: &param}}}}
	}
	cases := []struct {
		name    string
		change  func(*Config)
		invalid bool
	}{
		{"factory_only", func(*Config) {}, false},
		{"wildcard_factory_parents", func(c *Config) { c.Accounts[0].Factory.Address = nil }, false},
		{"multiple_factory_parents", func(c *Config) { c.Accounts[0].Factory.Address = []string{addr, addr} }, false},
		{"json_factory_parent_array", func(c *Config) { c.Accounts[0].Factory.Address = []any{addr} }, false},
		{"invalid_factory_parent", func(c *Config) { c.Accounts[0].Factory.Address = "0x123" }, true},
		{"empty_factory_parent_array", func(c *Config) { c.Accounts[0].Factory.Address = []string{} }, true},
		{"empty_json_factory_parent_array", func(c *Config) { c.Accounts[0].Factory.Address = []any{} }, true},
		{"non_string_factory_parent", func(c *Config) { c.Accounts[0].Factory.Address = []any{17} }, true},
		{"inherited_factory_end", func(c *Config) { c.Accounts[0].EndBlock = &from }, false},
		{"explicit_factory_end_after_account", func(c *Config) { c.Accounts[0].EndBlock = &to; c.Accounts[0].Factory.ToBlock = &from }, true},
		{"factory_start_after_account", func(c *Config) { c.Accounts[0].Factory.FromBlock = &from }, true},

		{"static_conflict", func(c *Config) { c.Accounts[0].Address = []string{addr} }, true},
		{"explicit_empty_static_conflict", func(c *Config) { c.Accounts[0].Address = []string{} }, true},
		{"wrong_abi_kind", func(c *Config) { c.Accounts[0].Factory.Event.Type = "function" }, true},
		{"empty_event_name", func(c *Config) { c.Accounts[0].Factory.Event.Name = "" }, true},
		{"invalid_parameter", func(c *Config) { wrong := "missing"; c.Accounts[0].Factory.Parameter = &wrong }, true},
		{"reversed_explicit_factory_bounds", func(c *Config) {
			c.Accounts[0].StartBlock = from
			c.Accounts[0].Factory.FromBlock = &from
			c.Accounts[0].Factory.ToBlock = &to
		}, true},
		{"inherited_factory_start_rejects_earlier_end", func(c *Config) { c.Accounts[0].StartBlock = 5; c.Accounts[0].Factory.ToBlock = &to }, true},
		{"explicit_earlier_factory_history", func(c *Config) {
			zero := uint64(0)
			c.Accounts[0].StartBlock = 5
			c.Accounts[0].Factory.FromBlock = &zero
		}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			c := valid()
			test.change(&c)
			before, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			err = c.Validate()
			if (err != nil) != test.invalid {
				t.Fatal("native factory configuration validation", err)
			}
			after, err := json.Marshal(c)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("validation mutated caller configuration", err)
			}
		})
	}
}
