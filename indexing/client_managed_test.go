package indexing

// Supplemental managed admission orchestration tests; no source-original credit.
// Controlled requester/cache futures exercise Client's actual hooks, not SQL.
import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
)

func managedCanceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func managedAwait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("managed operation failed to join")
		return nil
	}
}

func TestManagedClientRootsSealDescendantsAndLifetime(t *testing.T) {
	g := newManagedClientRoots()
	ctx, finish, err := g.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// A canceled waiter seals synchronously but cannot consume the active root.
	if err := g.sealAndWait(managedCanceledContext()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	g.mu.Lock()
	done, active := g.done, len(g.active)
	g.mu.Unlock()
	if active != 1 {
		t.Fatalf("canceled seal lost active root: %d", active)
	}
	select {
	case <-done:
		t.Fatal("seal falsely joined a running root")
	default:
	}
	if _, _, err := g.begin(context.Background()); err == nil {
		t.Fatal("sealed gate admitted new root")
	}
	child, childFinish, err := g.begin(ctx)
	if err != nil || child != ctx {
		t.Fatalf("admitted descendant lost canonical permit: %v", err)
	}
	childFinish()
	select {
	case <-done:
		t.Fatal("descendant leave retired its parent")
	default:
	}
	canceledChild, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := g.begin(canceledChild); !errors.Is(err, context.Canceled) {
		t.Fatal("descendant ignored cancellation", err)
	}
	finish()
	if err := g.sealAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.begin(ctx); err == nil {
		t.Fatal("retired canonical permit bypassed sealed admission")
	}
}

func TestManagedClientRootsStaleAndForeignPermits(t *testing.T) {
	g := newManagedClientRoots()
	old, finish, err := g.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finish()
	fresh, freshFinish, err := g.begin(old)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Value(managedClientPermitKey{}) == old.Value(managedClientPermitKey{}) {
		t.Fatal("stale permit resurrected")
	}
	freshFinish()
	foreign := newManagedClientRoots()
	foreignCtx, foreignFinish, err := foreign.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer foreignFinish()
	if err := g.sealAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.begin(foreignCtx); err == nil {
		t.Fatal("foreign permit bypassed sealed gate")
	}
}

type managedPendingCache struct {
	admitted chan context.Context
	complete chan error
}

func (c *managedPendingCache) CacheGet(context.Context, string) (json.RawMessage, bool, error) {
	return nil, false, nil
}
func (c *managedPendingCache) CachePut(context.Context, string, json.RawMessage) error {
	return errors.New("unexpected synchronous cache fallback")
}
func (c *managedPendingCache) BeginCachePut(ctx context.Context, _ string, _ json.RawMessage) <-chan error {
	c.admitted <- ctx
	return c.complete
}

func TestManagedClientRequestSealsBeforeCacheDescendantCompletes(t *testing.T) {
	entered, release := make(chan context.Context, 1), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	cache := &managedPendingCache{make(chan context.Context, 1), make(chan error, 1)}
	defer close(cache.complete)
	requester := admissionRequester(func(ctx context.Context, _ string, _, out any) error {
		entered <- ctx
		<-release
		return json.Unmarshal([]byte(`"0x2a"`), out)
	})
	chain := Object{"id": 1, "cacheRpcRequests": true}
	clients, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{requester}, cache)
	if err != nil {
		t.Fatal(err)
	}
	clients.managedRoots = newManagedClientRoots()
	client, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	caller, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		value, err := client.GetBalance(caller, "0xabc", BalanceOptions{BlockNumber: big.NewInt(1)})
		if err == nil && value.Int64() != 42 {
			err = errors.New("incorrect balance")
		}
		result <- err
	}()
	var requestCtx context.Context
	select {
	case requestCtx = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not enter")
	}
	if got, ok := requestCtx.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatal("admission replaced caller deadline")
	}
	if err := clients.managedRoots.sealAndWait(managedCanceledContext()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := client.GetBalance(context.Background(), "0xabc", BalanceOptions{BlockNumber: big.NewInt(1)}); err == nil {
		t.Fatal("escaped client admitted new request after seal")
	}
	unblock()
	if err := managedAwait(t, result); err != nil {
		t.Fatal(err)
	}
	var cacheCtx context.Context
	select {
	case cacheCtx = <-cache.admitted:
	default:
		t.Fatal("response returned without cache child admission")
	}
	if err := cacheCtx.Err(); err != nil {
		t.Fatal("root return canceled admitted cache child", err)
	}
	if got, ok := cacheCtx.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatal("cache child lost caller deadline")
	}
	if err := clients.managedRoots.sealAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := clients.WaitCacheWrites(managedCanceledContext()); !errors.Is(err, context.Canceled) {
		t.Fatal("root join falsely joined pending cache write", err)
	}
	// Root's retired context must not be a reusable admission capability.
	if _, _, err := clients.managedRoots.begin(requestCtx); err == nil {
		t.Fatal("completed request leaked admission capability")
	}
	cache.complete <- nil
	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := clients.WaitCacheWrites(wait); err != nil {
		t.Fatal(err)
	}
	if err := clients.Close(wait); err != nil {
		t.Fatal(err)
	}
}

func TestManagedClientMulticallRootIsJoinedAndSealed(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	marker := errors.New("controlled aggregate transport failure")
	requester := admissionRequester(func(context.Context, string, any, any) error {
		close(entered)
		<-release
		return marker
	})
	chain := Object{"id": 1}
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{requester})
	if err != nil {
		t.Fatal(err)
	}
	clients.managedRoots = newManagedClientRoots()
	client, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	abi := actionTestABI(t, `[{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]}]`)
	contracts := []ContractCall{{ABI: abi, Address: "0x0000000000000000000000000000000000000001", FunctionName: "value"}}
	allow, retry := false, false
	options := MulticallOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(1)}, MulticallAddress: "0x0000000000000000000000000000000000000002", AllowFailure: &allow, RetryEmptyResponse: &retry}
	result := make(chan error, 1)
	go func() { _, err := client.Multicall(context.Background(), contracts, options); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("aggregate request did not enter")
	}
	if err := clients.managedRoots.sealAndWait(managedCanceledContext()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	clients.managedRoots.mu.Lock()
	active := len(clients.managedRoots.active)
	clients.managedRoots.mu.Unlock()
	if active != 1 {
		t.Fatalf("multicall not retained as one active root: %d", active)
	}
	if _, err := client.Multicall(context.Background(), contracts, options); err == nil {
		t.Fatal("escaped multicall admitted after seal")
	}
	unblock()
	if err := managedAwait(t, result); !errors.Is(err, marker) {
		t.Fatal("multicall changed transport error", err)
	}
	if err := clients.managedRoots.sealAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The real ReadContract retry timer runs here. Causal requester channels, rather
// than sleeps, identify the first empty response and the subsequent retry.
func TestManagedReadContractRetryRetainsCanonicalRoot(t *testing.T) {
	firstEntered, retryEntered := make(chan context.Context, 1), make(chan context.Context, 1)
	firstRelease, retryRelease := make(chan struct{}), make(chan struct{})
	var firstOnce, retryOnce sync.Once
	releaseFirst := func() { firstOnce.Do(func() { close(firstRelease) }) }
	releaseRetry := func() { retryOnce.Do(func() { close(retryRelease) }) }
	defer releaseFirst()
	defer releaseRetry()
	calls := 0 // Actual ReadContract retries invoke this requester serially.
	requester := admissionRequester(func(ctx context.Context, _ string, _, out any) error {
		calls++
		if calls == 1 {
			firstEntered <- ctx
			<-firstRelease
			return json.Unmarshal([]byte(`"0x"`), out)
		}
		retryEntered <- ctx
		<-retryRelease
		return json.Unmarshal([]byte(`"0x000000000000000000000000000000000000000000000000000000000000002a"`), out)
	})
	chain := Object{"id": 1}
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{requester})
	if err != nil {
		t.Fatal(err)
	}
	clients.managedRoots = newManagedClientRoots()
	client, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	abi := actionTestABI(t, `[{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]}]`)
	contract := ContractCall{ABI: abi, Address: "0x0000000000000000000000000000000000000001", FunctionName: "value"}
	options := ReadContractOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(1)}}
	deadline := time.Now().Add(time.Minute)
	caller, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		value, err := client.ReadContract(caller, contract, options)
		if err == nil {
			n, ok := value.(*big.Int)
			if !ok || n.Int64() != 42 {
				err = errors.New("incorrect retry result")
			}
		}
		finished <- err
	}()
	var first context.Context
	select {
	case first = <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not enter")
	}
	if err := clients.managedRoots.sealAndWait(managedCanceledContext()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	clients.managedRoots.mu.Lock()
	joined := clients.managedRoots.done
	clients.managedRoots.mu.Unlock()
	rejected := make(chan error, 1)
	go func() { _, err := client.ReadContract(context.Background(), contract, options); rejected <- err }()
	if err := managedAwait(t, rejected); err == nil {
		t.Fatal("new ReadContract root entered after seal")
	}
	releaseFirst()
	var retry context.Context
	select {
	case retry = <-retryEntered:
	case err := <-finished:
		t.Fatal("admitted retry failed before transport", err)
	case <-time.After(5 * time.Second):
		t.Fatal("admitted retry did not enter")
	}
	if first.Value(managedClientPermitKey{}) != retry.Value(managedClientPermitKey{}) {
		t.Fatal("retry replaced canonical root permit")
	}
	if got, ok := retry.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatal("retry replaced caller deadline")
	}
	// The SAME root completion channel must stay pending across the empty
	// response and retry wait, not briefly close and reopen between attempts.
	select {
	case <-joined:
		t.Fatal("root joined between retry attempts")
	default:
	}
	clients.managedRoots.mu.Lock()
	active := len(clients.managedRoots.active)
	clients.managedRoots.mu.Unlock()
	if active != 1 {
		t.Fatalf("retry lost outer root: %d", active)
	}
	releaseRetry()
	if err := managedAwait(t, finished); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("unexpected retry attempts: %d", calls)
	}
	if err := clients.managedRoots.sealAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := clients.managedRoots.begin(retry); err == nil {
		t.Fatal("retired retry context bypassed seal")
	}
}

func TestManagedClientRetainsFirstAsyncCacheFailureOnly(t *testing.T) {
	for _, managed := range []bool{false, true} {
		name := "legacy"
		if managed {
			name = "managed"
		}
		t.Run(name, func(t *testing.T) {
			first, second := errors.New("first admitted cache failure"), errors.New("later admitted cache failure")
			cache := &managedPendingCache{make(chan context.Context, 1), make(chan error, 1)}
			defer close(cache.complete)
			requester := admissionRequester(func(_ context.Context, _ string, _, out any) error {
				return json.Unmarshal([]byte(`"0x2a"`), out)
			})
			chain := Object{"id": 1, "cacheRpcRequests": true}
			clients, err := NewStrictClientSet([]Object{chain}, []rpc.Requester{requester}, cache)
			if err != nil {
				t.Fatal(err)
			}
			if managed {
				clients.managedRoots = newManagedClientRoots()
			}
			client, err := clients.GetClient(chain)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, failure := range []error{first, second} {
				value, err := client.GetBalance(ctx, "0xabc", BalanceOptions{BlockNumber: big.NewInt(1)})
				if err != nil || value.Int64() != 42 {
					t.Fatal("write failure altered synchronous RPC result", value, err)
				}
				select {
				case <-cache.admitted:
				default:
					t.Fatal("response returned before actual cache future admission")
				}
				cache.complete <- failure
				if err := clients.WaitCacheWrites(ctx); err != nil {
					t.Fatal(err)
				}
				got := clients.managedRoots.failure()
				if managed && !errors.Is(got, first) {
					t.Fatal("managed join lost/replaced first async error", got)
				}
				if !managed && got != nil {
					t.Fatal("legacy nil gate changed async error policy", got)
				}
			}
			if err := clients.managedRoots.sealAndWait(ctx); err != nil {
				t.Fatal(err)
			}
			if managed && !errors.Is(clients.managedRoots.failure(), first) {
				t.Fatal("sealing erased asynchronous failure")
			}
			if err := clients.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
