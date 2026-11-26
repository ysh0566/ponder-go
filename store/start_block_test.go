package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/schema"
)

func TestResolveStartBlock(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			db := sourceBackend(t, ctx, backend)
			ns := fmt.Sprintf("ponder_go_start_%x", time.Now().UnixNano())
			s, err := New(ctx, db, model(false), ns, schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			offline := errors.New("node offline")
			fail := func(context.Context) (uint64, error) { return 0, offline }
			if _, err := s.ResolveStartBlock(ctx, 1, fail); !errors.Is(err, offline) {
				t.Fatal(err)
			}
			// The second starter obtains and saves its head while the first is still reading RPC.
			got, err := s.ResolveStartBlock(ctx, 1, func(ctx context.Context) (uint64, error) {
				pinned, err := s.ResolveStartBlock(ctx, 1, func(context.Context) (uint64, error) { return 0, nil })
				if err != nil || pinned != 0 {
					t.Fatalf("second starter: %d, %v", pinned, err)
				}
				return 2, nil
			})
			if err != nil || got != 0 {
				t.Fatal(got, err)
			}
			got, err = s.ResolveStartBlock(ctx, 1, fail)
			if err != nil || got != 0 {
				t.Fatal(got, err)
			}
			got, err = s.ResolveStartBlock(ctx, 2, func(context.Context) (uint64, error) { return 5, nil })
			if err != nil || got != 5 {
				t.Fatal(got, err)
			}
		})
	}
}
