package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"
)

// Synchronous escaped requests are not ClientSet's prefetch futures. The owner
// must still retain them, and cannot close storage underneath a slow requester.
func TestOwnedCloseJoinsEscapedRequest(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			requester := admissionRequester(func(ctx context.Context, method string, params, out any) error {
				close(entered)
				<-release // Deliberately ignores cancellation: ownership must remain.
				return json.Unmarshal([]byte(`"0x2a"`), out)
			})
			f := newOwnedFixture(t, dialect, Options{Build: Build{Chains: []Object{{"id": 1, "cacheRpcRequests": true}}, Contracts: []Object{{}}}}, requester)
			o := f.start(t)
			c, err := o.Clients().GetClient(Object{"id": 1})
			if err != nil {
				t.Fatal(err)
			}
			requestDone := make(chan error, 1)
			go func() {
				_, err := c.GetBalance(context.Background(), "0x0000000000000000000000000000000000000001", BalanceOptions{BlockNumber: big.NewInt(1)})
				requestDone <- err
			}()
			<-entered
			wait, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			err = o.Close(wait)
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("bounded close=%v", err)
			}
			if _, err := o.storage.BusinessStore().Backend.Query(context.Background(), "SELECT 1 AS alive"); err != nil {
				t.Fatalf("backend closed under owned request: %v", err)
			}
			if err = o.Database().WithReadOnly(context.Background(), 1, func(context.Context) error { return nil }); err == nil {
				t.Fatal("readonly work admitted after close")
			}
			close(release)
			<-requestDone
			wait, cancel = context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err = o.Close(wait); err != nil {
				t.Fatal(err)
			}
			if err = o.Close(wait); err != nil {
				t.Fatal("repeat join", err)
			}
		})
	}
}
