package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceTransport(t *testing.T) {
	t.Run("retry_null_and_connection", func(t *testing.T) {
		var calls atomic.Int32
		client, err := NewSourceTransportClient(1, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 10*time.Second || method != "eth_getBlockByNumber" {
				t.Errorf("unexpected attempt: method=%s deadline=%v", method, deadline)
			}
			switch calls.Add(1) {
			case 1:
				return nil, io.ErrUnexpectedEOF
			case 2:
				return json.RawMessage("  null  "), nil
			}
			return json.RawMessage(`{"number":"0x1"}`), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		client.Retries = 2
		var block map[string]string
		done, owned := client.WithCache(nil).BeginSourceCall(t.Context(), "eth_getBlockByNumber", []any{"0x1", false}, &block, RequestOptions{RetryNullBlockRequest: true})
		if !owned {
			t.Fatal("request not admitted by source client")
		}
		if err := <-done; err != nil || block["number"] != "0x1" || calls.Load() != 3 || client.TransportRequests() != 3 {
			t.Fatalf("result=%v attempts=%d count=%d error=%v", block, calls.Load(), client.TransportRequests(), err)
		}
	})
	t.Run("errors_and_cache", func(t *testing.T) {
		denied := &Error{Code: -32601, Message: "unknown method"}
		client, err := NewSourceTransportClient(1, func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			switch method {
			case "bad":
				return json.RawMessage("invalid"), nil
			case "absent":
				return json.RawMessage(" \n null"), nil
			case "ok":
				return json.RawMessage(`"0x2"`), nil
			default:
				return nil, denied
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		cache := &memoryCache{values: map[string]json.RawMessage{}}
		client.ChainID = 202
		view := client.WithCache(cache)
		var out any
		if err := view.Call(t.Context(), "unknown", nil, &out); !errors.Is(err, denied) || view.TransportRequests() != 1 {
			t.Fatalf("permanent error: %v, attempts=%d", err, view.TransportRequests())
		}
		client.Retries = 0
		if err := view.CallCached(t.Context(), "bad", nil, &out); err == nil {
			t.Fatal("invalid result accepted")
		}
		for _, name := range []string{"absent", "absent", "ok", "ok"} {
			if err := view.CallCached(t.Context(), name, nil, &out); err != nil {
				t.Fatal(err)
			}
		}
		if len(cache.values) != 1 || string(cache.values[`[202,"ok",null]`]) != `"0x2"` || out != "0x2" || client.TransportRequests() != 5 {
			t.Fatal("cache or attempt count", cache.values, out, client.TransportRequests())
		}
	})
	t.Run("transport_timeout", func(t *testing.T) {
		client, err := NewSourceTransportClient(1, func(context.Context, string, any) (json.RawMessage, error) {
			return nil, context.DeadlineExceeded
		})
		if err != nil {
			t.Fatal(err)
		}
		client.Retries = 0
		var out any
		err = client.Call(t.Context(), "eth_chainId", nil, &out)
		if !errors.Is(err, context.DeadlineExceeded) || !IsTransient(err) {
			t.Fatalf("transport deadline should allow acquisition retry: %v", err)
		}
	})
	t.Run("shared_admission_and_cancellation", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		client, err := NewSourceTransportClient(1, func(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
			entered <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var first, second any
		running := client.BeginCall(ctx, "first", nil, &first)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("first call never started")
		}
		queued, stop := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer stop()
		if err := client.WithCache(nil).Call(queued, "second", nil, &second); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		cancel()
		if err := <-running; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if client.TransportRequests() != 1 || len(entered) != 0 {
			t.Fatal("second call reached transport")
		}
	})
}
