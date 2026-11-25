package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/factory"
)

// ApplicationDeclaration retains the caller's concrete chain, contract, account
// and block collections. Struct fields with JSON tags preserve declaration order.
type ApplicationDeclaration[C, K, A, B any] struct {
	Database  any    `json:"database,omitempty"`
	Ordering  string `json:"ordering,omitempty"`
	Chains    C      `json:"chains"`
	Contracts *K     `json:"contracts,omitempty"`
	Accounts  *A     `json:"accounts,omitempty"`
	Blocks    *B     `json:"blocks,omitempty"`
}

// CreateConfig is the source declaration boundary. Like its source counterpart,
// it returns the original definition, including the caller's concrete types.
func CreateConfig[C, K, A, B any](definition *ApplicationDeclaration[C, K, A, B]) *ApplicationDeclaration[C, K, A, B] {
	return definition
}

// Source converts a JSON-backed declaration into the configuration consumed
// by the native source builder. Encoding errors precede any runtime startup.
func (a ApplicationDeclaration[C, K, A, B]) Source() (*SourceConfiguration, error) {
	data, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return ParseSourceConfiguration(data)
}

type ChainDeclaration[I, R any] struct {
	ID                   I        `json:"id"`
	RPC                  R        `json:"rpc"`
	WS                   string   `json:"ws,omitempty"`
	PollingInterval      *float64 `json:"pollingInterval,omitempty"`
	ReorgWindow          *float64 `json:"reorgWindow,omitempty"`
	MaxRequestsPerSecond *float64 `json:"maxRequestsPerSecond,omitempty"`
	CacheRPCRequests     *bool    `json:"cacheRpcRequests,omitempty"`
	EthGetLogsBlockRange *float64 `json:"ethGetLogsBlockRange,omitempty"`
}

// ContractDeclaration accepts a single chain or a collection of per-chain
// overrides. F can be a single filter or a list of filters.
type ContractDeclaration[I, C, F any] struct {
	ABI                        I     `json:"abi"`
	Chain                      C     `json:"chain"`
	Address                    any   `json:"address,omitempty"`
	StartBlock                 any   `json:"startBlock,omitempty"`
	EndBlock                   any   `json:"endBlock,omitempty"`
	Filter                     *F    `json:"filter,omitempty"`
	IncludeTransactionReceipts *bool `json:"includeTransactionReceipts,omitempty"`
	IncludeCallTraces          *bool `json:"includeCallTraces,omitempty"`
}

type ContractOverrideDeclaration[F any] struct {
	Address                    any   `json:"address,omitempty"`
	StartBlock                 any   `json:"startBlock,omitempty"`
	EndBlock                   any   `json:"endBlock,omitempty"`
	Filter                     *F    `json:"filter,omitempty"`
	IncludeTransactionReceipts *bool `json:"includeTransactionReceipts,omitempty"`
	IncludeCallTraces          *bool `json:"includeCallTraces,omitempty"`
}

type AccountDeclaration[C, A any] struct {
	Chain                      C     `json:"chain"`
	Address                    A     `json:"address"`
	StartBlock                 any   `json:"startBlock,omitempty"`
	EndBlock                   any   `json:"endBlock,omitempty"`
	IncludeTransactionReceipts *bool `json:"includeTransactionReceipts,omitempty"`
}

type EventFilterDeclaration[E, A any] struct {
	Event E  `json:"event"`
	Args  *A `json:"args,omitempty"`
}

func DeclareEventFilter[E, A any](event E, args A) EventFilterDeclaration[E, A] {
	return EventFilterDeclaration[E, A]{Event: event, Args: &args}
}

type FactoryEvent interface{ Definition() abi.Event }
type FactoryParameter[E FactoryEvent] interface{ ParameterOf(E) string }

// DynamicFactoryParameter names a field in an event whose ABI is loaded at runtime.
type DynamicFactoryParameter string

func (p DynamicFactoryParameter) ParameterOf(abi.Event) string { return string(p) }

type FactoryOptions struct {
	Address    any `json:"address,omitempty"`
	StartBlock any `json:"startBlock,omitempty"`
	EndBlock   any `json:"endBlock,omitempty"`
}

// FactoryDeclaration preserves the source event and its selected parameter or
// low-level location. The native builder resolves the event's wire location.
type FactoryDeclaration[E FactoryEvent] struct {
	FactoryOptions
	Event               E `json:"event"`
	parameter, location string
}

func (f FactoryDeclaration[E]) metadata() (any, error) {
	if nilFactoryValue(f.Event) || (f.parameter == "") == (f.location == "") {
		return nil, fmt.Errorf("factory requires an event and exactly one parameter or location")
	}
	def := f.Event.Definition()
	if def.Type == "" && def.Name == "" && def.Inputs == nil && !def.Anonymous {
		return struct{}{}, nil
	}
	if def.Type != "event" {
		return nil, fmt.Errorf("factory requires an ABI event, got %q", def.Type)
	}
	if f.parameter != "" && !slices.Contains(def.FactoryParameterPaths(), f.parameter) {
		return nil, fmt.Errorf("unknown factory parameter %q on %s", f.parameter, def.Name)
	}
	return def, nil
}

func (f FactoryDeclaration[E]) MarshalJSON() ([]byte, error) {
	metadata, err := f.metadata()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		FactoryOptions
		Event     any    `json:"event"`
		Parameter string `json:"parameter,omitempty"`
		Location  string `json:"location,omitempty"`
	}{f.FactoryOptions, metadata, f.parameter, f.location})
}

// Generated event and parameter types have value receivers. A nil pointer to
// one satisfies the interface but cannot supply that value.
func nilFactoryValue(value any) bool {
	return value == nil || (reflect.ValueOf(value).Kind() == reflect.Pointer && reflect.ValueOf(value).IsNil())
}

func FactoryByParameter[E FactoryEvent](event E, parameter FactoryParameter[E], opts FactoryOptions) (FactoryDeclaration[E], error) {
	var zero FactoryDeclaration[E]
	if nilFactoryValue(event) {
		return zero, fmt.Errorf("factory event is missing")
	}
	if nilFactoryValue(parameter) {
		return zero, fmt.Errorf("factory event parameter is missing")
	}
	path := parameter.ParameterOf(event)
	if path == "" {
		return zero, fmt.Errorf("factory event parameter is empty")
	}
	f := FactoryDeclaration[E]{FactoryOptions: opts, Event: event, parameter: path}
	if _, err := f.metadata(); err != nil {
		return zero, err
	}
	return f, nil
}

// FactoryByLocation selects a raw topic or data offset instead of a parameter.
func FactoryByLocation[E FactoryEvent](event E, location string, opts FactoryOptions) (FactoryDeclaration[E], error) {
	if err := factory.ValidateLocation(location); err != nil {
		return FactoryDeclaration[E]{}, err
	}
	f := FactoryDeclaration[E]{FactoryOptions: opts, Event: event, location: location}
	if _, err := f.metadata(); err != nil {
		return FactoryDeclaration[E]{}, err
	}
	return f, nil
}
