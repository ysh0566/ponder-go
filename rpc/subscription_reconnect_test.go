package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSubscriptionReconnect(t *testing.T) {
	var sessions atomic.Int64
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var req struct {
			Method string
			Params []string
		}
		if err := ws.ReadJSON(&req); err != nil || req.Method != "eth_subscribe" || len(req.Params) != 1 || req.Params[0] != "newHeads" {
			t.Errorf("subscribe request %+v %v", req, err)
			return
		}
		n := sessions.Add(1)
		id := "subscription-one"
		if n > 1 {
			id = "subscription-two"
		}
		if err := ws.WriteJSON(map[string]any{"id": 1, "result": id}); err != nil {
			return
		}
		send := func(sub string, value int64) {
			_ = ws.WriteJSON(map[string]any{"method": "eth_subscription", "params": map[string]any{"subscription": sub, "result": value}})
		}
		send("unrelated", -1)
		send(id, n)
		if n > 1 {
			_, _, _ = ws.ReadMessage()
		}
	}))
	defer server.Close()
	var refused atomic.Int64
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refused.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	wsURL := func(url string) string { return "ws" + strings.TrimPrefix(url, "http") }
	sub, err := New(wsURL(unavailable.URL), wsURL(server.URL)).Subscribe(ctx, "newHeads")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{1, 2} {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case raw, ok := <-sub:
			var got int64
			if !ok || json.Unmarshal(raw, &got) != nil || got != want {
				t.Fatalf("notification %s want %d", raw, want)
			}
		}
	}
	cancel()
	if refused.Load() < 2 {
		t.Fatalf("expected initial and reconnect failover, got %d attempts", refused.Load())
	}
	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("notification after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled subscription still active")
	}
}

func TestSubscriptionPermanentPolicyClose(t *testing.T) {
	var sessions atomic.Int64
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		sessions.Add(1)
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		if err := ws.WriteJSON(map[string]any{"id": 1, "result": "sub"}); err != nil {
			return
		}
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "subscription denied"), time.Now().Add(time.Second))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	sub, err := New("ws"+strings.TrimPrefix(server.URL, "http")).Subscribe(ctx, "newHeads")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-sub:
		if ok || sessions.Load() != 1 {
			t.Fatal("subscription survived policy rejection")
		}
	case <-ctx.Done():
		t.Fatal("policy rejection did not end subscription")
	}
}
