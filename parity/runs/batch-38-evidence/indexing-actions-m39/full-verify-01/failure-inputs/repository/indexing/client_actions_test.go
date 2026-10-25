package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
)

func TestActionEmptyRetryPolicy(t *testing.T) {
	calls := 0
	delays := []time.Duration{}
	_, err := retryEmptyWithWait(context.Background(), nil, func() (int, error) { calls++; return 0, &ContractFunctionZeroDataError{"supply"} }, func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil })
	var zero *ContractFunctionZeroDataError
	if !errors.As(err, &zero) || calls != 10 || len(delays) != 9 {
		t.Fatal(calls, delays, err)
	}
	for i, d := range delays {
		if d != 125*time.Millisecond*time.Duration(1<<i) {
			t.Fatal(i, d)
		}
	}
	disabled := false
	calls = 0
	_, err = retryEmpty(context.Background(), &disabled, func() (int, error) { calls++; return 0, &ContractFunctionZeroDataError{"supply"} })
	if !errors.As(err, &zero) || calls != 1 {
		t.Fatal(calls, err)
	}
	calls = 0
	other := errors.New("ordinary transport error")
	_, err = retryEmpty(context.Background(), nil, func() (int, error) { calls++; return 0, other })
	if err != other || calls != 1 {
		t.Fatal(calls, err)
	}
	calls = 0
	value, err := retryEmptyWithWait(context.Background(), nil, func() (int, error) {
		calls++
		if calls == 1 {
			return 0, errors.New("wrapped transport returned no data")
		}
		return 42, nil
	}, func(context.Context, time.Duration) error { return nil })
	if err != nil || value != 42 || calls != 2 {
		t.Fatal("source message-based retry", value, calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls = 0
	_, err = retryEmpty(ctx, nil, func() (int, error) { calls++; cancel(); return 0, &ContractFunctionZeroDataError{"supply"} })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(calls, err)
	}
}
func TestActionCacheCanonicalizationAndEmptyExclusion(t *testing.T) {
	c := &Client{chainID: json.Number("1")}
	a, err := c.cacheKey("eth_call", []any{Object{"to": "0xABC", "data": "0xDEF", "nested": Object{"Z": "VALUE", "A": "OTHER"}}, "0xF"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.cacheKey("eth_call", []any{Object{"nested": Object{"A": "other", "Z": "value"}, "data": "0xdef", "to": "0xabc"}, "0xf"})
	if err != nil || a != b {
		t.Fatal(a, b, err)
	}
	c.chainID = json.Number("2")
	other, _ := c.cacheKey("eth_call", []any{Object{"nested": Object{"A": "other", "Z": "value"}, "data": "0xdef", "to": "0xabc"}, "0xf"})
	if a == other {
		t.Fatal("chain cache collision")
	}
	for _, raw := range []string{`null`, `"0x"`, ``} {
		if cacheableResponse(json.RawMessage(raw)) {
			t.Fatal("empty cached", raw)
		}
	}
	if !cacheableResponse(json.RawMessage(`"0x00"`)) {
		t.Fatal("nonempty response rejected")
	}
}
func TestStrictClientRejectsSynchronousOnlyCache(t *testing.T) {
	chain := Object{"id": 1, "cacheRpcRequests": true}
	requesters := []rpc.Requester{&balanceRequester{}}
	if _, err := NewStrictClientSet([]Object{chain}, requesters, &balanceCache{}); err == nil {
		t.Fatal("synchronous adapter accepted")
	}
	if _, err := NewStrictClientSet([]Object{chain}, requesters); err == nil {
		t.Fatal("missing adapter accepted")
	}
	if _, err := NewStrictClientSet([]Object{{"id": 1}}, requesters); err != nil {
		t.Fatal(err)
	}
}
