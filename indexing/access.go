// Package indexing implements the source-shaped handler service. Historical
// event properties use explicit accessors in Go, rather than pretending that
// ordinary Go map reads can intercept the JavaScript Proxy operations.
package indexing

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"ponder.local/ponder-go/filter"
)

type Object = map[string]any

// RetryableErrorHandler preserves errors even if a user callback catches an
// accessor or database error. The service checks it after callback completion.
type RetryableErrorHandler interface {
	GetRetryableError() error
	SetRetryableError(error)
	ClearRetryableError()
}

// InvalidEventAccessError has an empty default message, just like the pinned
// source error. Key is separate from Message; callers must not fabricate a
// message containing Key when comparing source errors.
type InvalidEventAccessError struct {
	Key     string
	Message string
}

func (e *InvalidEventAccessError) Error() string     { return e.Message }
func (e *InvalidEventAccessError) Retryable() bool   { return true }
func (e *InvalidEventAccessError) ErrorName() string { return "InvalidEventAccessError" }

// ColumnAccessProfile represents the four source insertion-ordered Sets. The
// slices preserve first-access order, which is observable in filter.include.
// The service and its synchronous callback own this state exclusively.
type ColumnAccessProfile struct {
	Block              []string `json:"block"`
	Transaction        []string `json:"transaction"`
	TransactionReceipt []string `json:"transactionReceipt"`
	Trace              []string `json:"trace"`
	Resolved           bool     `json:"resolved"`
	Count              int      `json:"count"`
}

type ColumnAccessPattern map[string]*ColumnAccessProfile

func NewColumnAccessPattern(eventNames []string) ColumnAccessPattern {
	pattern := make(ColumnAccessPattern, len(eventNames))
	for _, name := range eventNames {
		pattern[name] = &ColumnAccessProfile{
			Block: []string{}, Transaction: []string{},
			TransactionReceipt: []string{}, Trace: []string{},
		}
	}
	return pattern
}

func (p *ColumnAccessProfile) columns(component string) *[]string {
	switch component {
	case "block":
		return &p.Block
	case "transaction":
		return &p.Transaction
	case "transactionReceipt":
		return &p.TransactionReceipt
	case "trace":
		return &p.Trace
	default:
		return nil
	}
}

var accessDefaultColumns = map[string][]string{
	"block":              strings.Fields("baseFeePerGas difficulty extraData gasLimit gasUsed hash logsBloom miner mixHash totalDifficulty nonce number parentHash receiptsRoot sha3Uncles size stateRoot timestamp transactionsRoot"),
	"transaction":        strings.Fields("from gas hash input nonce r s to transactionIndex v value type gasPrice accessList maxFeePerGas maxPriorityFeePerGas"),
	"transactionReceipt": strings.Fields("contractAddress cumulativeGasUsed effectiveGasPrice from gasUsed logsBloom status to type"),
	"trace":              strings.Fields("traceAddress type from to gas gasUsed input output value"),
	"log":                strings.Fields("address data logIndex removed topics"),
}

var accessRequiredColumns = map[string][]string{
	"block":              strings.Fields("timestamp number hash"),
	"transaction":        strings.Fields("transactionIndex from to hash type"),
	"transactionReceipt": strings.Fields("status from to"),
	"trace":              strings.Fields("traceAddress type from to input output value"),
	"log":                strings.Fields("address data logIndex removed topics"),
}

// PropertyView is a stable proxy identity whose binding changes between
// historical callbacks. Like the source proxy, retaining it retains the view,
// not a snapshot of its current underlying event. It is not safe for concurrent
// use: the service owns its binding and its callback accesses synchronously.
type PropertyView struct {
	component  string
	pattern    ColumnAccessPattern
	handler    RetryableErrorHandler
	reset      func(string)
	eventName  string
	underlying Object
	disabled   bool
	// onAccess is an optional package diagnostic observer at the actual tracking
	// point, before presence validation; repeated accesses remain observable.
	onAccess func(eventName, component, key string)
}

func NewPropertyView(component string, pattern ColumnAccessPattern, handler RetryableErrorHandler, reset func(string)) (*PropertyView, error) {
	if component != "block" && component != "transaction" && component != "transactionReceipt" && component != "trace" {
		return nil, fmt.Errorf("unsupported event proxy component %q", component)
	}
	if pattern == nil || reset == nil || isNilAccessHandler(handler) {
		return nil, fmt.Errorf("event proxy requires a column pattern, retryable error handler, and reset callback")
	}
	return &PropertyView{component: component, pattern: pattern, handler: handler, reset: reset}, nil
}

func isNilAccessHandler(handler RetryableErrorHandler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (v *PropertyView) Bind(eventName string, underlying Object) {
	v.eventName, v.underlying = eventName, underlying
}

// SetDisabled is the per-service equivalent of the source's diagnostic-only
// DISABLE_EVENT_PROXY switch. It must only be changed by the owning service
// outside a callback, and restored before user code runs. Has and Keys retain
// their source behavior even while disabled. It does not expose the raw map.
func (v *PropertyView) SetDisabled(disabled bool) { v.disabled = disabled }

func (v *PropertyView) checkAccess(key string) error {
	if v.underlying == nil {
		return fmt.Errorf("event proxy %s is not bound to an object", v.component)
	}
	if v.disabled || !v.Has(key) {
		return nil
	}
	profile := v.pattern[v.eventName]
	if profile == nil {
		return fmt.Errorf("column access profile %q is not initialized", v.eventName)
	}
	if v.onAccess != nil {
		v.onAccess(v.eventName, v.component, key)
	}
	columns := profile.columns(v.component)
	if !slices.Contains(*columns, key) {
		*columns = append(*columns, key)
	}
	if _, present := v.underlying[key]; !present {
		profile.Resolved = false
		v.reset(v.eventName)
		err := &InvalidEventAccessError{Key: v.component + "." + key}
		v.handler.SetRetryableError(err)
		return err
	}
	return nil
}

func (v *PropertyView) Get(key string) (any, error) {
	if err := v.checkAccess(key); err != nil {
		return nil, err
	}
	if value, present := v.underlying[key]; present {
		return value, nil
	}
	return filter.Undefined, nil
}

func (v *PropertyView) Set(key string, value any) error {
	if err := v.checkAccess(key); err != nil {
		return err
	}
	v.underlying[key] = value
	return nil
}

func (v *PropertyView) Delete(key string) error {
	if err := v.checkAccess(key); err != nil {
		return err
	}
	delete(v.underlying, key)
	return nil
}

// Has mirrors the source Proxy.has trap: membership is in the default column
// set, not in the underlying object, and it never records an access.
func (v *PropertyView) Has(key string) bool {
	return slices.Contains(accessDefaultColumns[v.component], key)
}

// Keys mirrors Reflect.ownKeys, not Object.keys (the source proxy does not
// provide descriptors for the virtual keys). Returned keys cannot mutate the
// view's default set.
func (v *PropertyView) Keys() []string {
	return append([]string{}, accessDefaultColumns[v.component]...)
}

// Inspect performs the same all-column reads as the source custom inspection
// hook. A missing column can therefore invalidate a profile and return an error.
func (v *PropertyView) Inspect() (Object, error) {
	out := Object{}
	for _, key := range v.Keys() {
		value, err := v.Get(key)
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func prefixedColumns(component string, columns []string) []string {
	out := make([]string, len(columns))
	for i, column := range columns {
		out[i] = component + "." + column
	}
	return out
}

func filterInclude(kind string, receipts bool, columns map[string][]string) []string {
	out := []string{}
	add := func(component string) { out = append(out, prefixedColumns(component, columns[component])...) }
	switch kind {
	case "block":
		add("block")
	case "log":
		add("log")
		add("transaction")
		add("block")
	case "transaction":
		add("transaction")
		add("transactionReceipt")
		add("block")
	case "trace", "transfer":
		add("block")
		add("transaction")
		add("trace")
	default:
		return nil
	}
	if receipts && (kind == "log" || kind == "trace" || kind == "transfer") {
		add("transactionReceipt")
	}
	return out
}

// DefaultFilterInclude follows resetFilterInclude's exact ordering.
func DefaultFilterInclude(kind string, receipts bool) []string {
	return filterInclude(kind, receipts, accessDefaultColumns)
}

func RequiredFilterInclude(kind string, receipts bool) []string {
	return filterInclude(kind, receipts, accessRequiredColumns)
}

// ResolveProfileInclude appends first-access sets in source order, then the
// required columns, then deduplicates while retaining their first occurrence.
// The service owns the 100-sample threshold and profile/filter resolved flags.
func ResolveProfileInclude(profile *ColumnAccessProfile, kind string, receipts bool) []string {
	if profile == nil {
		return nil
	}
	out := []string{}
	for _, component := range []string{"block", "transaction", "transactionReceipt", "trace"} {
		out = append(out, prefixedColumns(component, *profile.columns(component))...)
	}
	out = append(out, RequiredFilterInclude(kind, receipts)...)
	seen := make(map[string]bool, len(out))
	unique := make([]string, 0, len(out))
	for _, column := range out {
		if !seen[column] {
			unique = append(unique, column)
			seen[column] = true
		}
	}
	return unique
}
