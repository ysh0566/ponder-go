package indexing

import (
	"errors"
	"math/big"
	"slices"
	"testing"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/filter"
)

func TestTypedEventTrackedFields(t *testing.T) {
	const name = "token:Transfer"
	pattern := NewColumnAccessPattern([]string{name})
	state := new(ErrorState)
	reset := false
	proxy, err := NewPropertyView("block", pattern, state, func(string) { reset = true })
	if err != nil {
		t.Fatal(err)
	}
	proxy.Bind(name, Object{"number": big.NewInt(37)})
	body := EventView{"id": "1", "block": proxy}
	block, err := body.BlockFields()
	if err != nil || len(pattern[name].Block) != 0 {
		t.Fatal(err, pattern[name])
	}
	got, err := block.Number()
	if err != nil || got.Int64() != 37 || !slices.Equal(pattern[name].Block, []string{"number"}) {
		t.Fatal(got, err, pattern[name])
	}
	got.SetInt64(0)
	n, err := block.Number()
	if err != nil || n.Int64() != 37 {
		t.Fatal(n, err)
	}
	proxy.Bind(name, Object{"number": big.NewInt(41)})
	n, err = block.Number()
	if err != nil || n.Int64() != 41 {
		t.Fatal(n, err)
	}
	_, err = block.GasUsed()
	var invalid *InvalidEventAccessError
	if !errors.As(err, &invalid) || state.GetRetryableError() != err || !reset {
		t.Fatal(err, state.GetRetryableError(), reset)
	}
}

func TestTypedEventOptionalAndRequiredFields(t *testing.T) {
	body := EventView{"transactionReceipt": filter.Undefined, "block": Object{"number": "malformed", "mixHash": nil}, "trace": Object{"output": "0x1234", "traceAddress": []any{1, 2}}}
	if p, err := body.OptionalReceipt(); p != nil || err != nil {
		t.Fatal(p, err)
	}
	if _, err := body.ReceiptFields(); err == nil {
		t.Fatal("accepted missing receipt")
	}
	block, err := body.BlockFields()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = block.Number(); err == nil {
		t.Fatal("accepted invalid block number")
	}
	if n, err := block.MixHash(); err != nil || n != nil {
		t.Fatal(n, err)
	}
	trace, err := body.TraceFields()
	if err != nil {
		t.Fatal(err)
	}
	if hex, err := trace.Output(); err != nil || hex == nil || *hex != abi.Hex("0x1234") {
		t.Fatal(hex, err)
	}
	if index, err := trace.TraceAddress(); err != nil || !slices.Equal(index, []uint64{1, 2}) {
		t.Fatal(index, err)
	}
}
