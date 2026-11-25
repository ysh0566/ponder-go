package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/rpc"
)

type ensVector struct {
	Name     string
	Key      *string
	Coin     *int64
	Gateways []string
	Want     *string
	Setter   string
}

func TestCallbackENSRecordsAtEvent(t *testing.T) {
	const dir = "../testdata/indexing/ens/"
	var fixture struct {
		Resolver    string
		Records     []ensVector
		Replacement ensVector
	}
	data, err := os.ReadFile(dir + "records.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(dir + "RecordResolver.hex")
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			net := newSourceClientChain(t, sourceChainContracts{})
			var ignored any
			net.call("anvil_setCode", []any{fixture.Resolver, strings.TrimSpace(string(code))}, &ignored)
			for _, r := range fixture.Records {
				net.call("eth_sendTransaction", []any{Object{"from": sourceClientAlice, "to": fixture.Resolver, "gas": "0x800000", "data": r.Setter}}, &ignored)
			}
			net.mine()
			var snapshot string
			net.call("evm_snapshot", []any{}, &snapshot)
			net.send(fixture.Resolver, fixture.Replacement.Setter)
			chain := Object{"name": "local", "id": 1, "cacheRpcRequests": true, "viemChain": Object{
				"ensTlds": []string{".eth"}, "contracts": Object{"ensUniversalResolver": Object{"address": fixture.Resolver, "blockCreated": 1}}}}
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{net.rpc}, adapterStore(t, dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			runClientCallback(t, ctx, dialect, chain, clients, func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				check := func(r ensVector, opt ENSNameOptions) error {
					opt.GatewayURLs = r.Gateways
					if r.Coin != nil {
						opt.CoinType = big.NewInt(*r.Coin)
					}
					var got *string
					var err error
					if r.Key == nil {
						got, err = c.GetEnsAddress(ctx, r.Name, opt)
					} else {
						got, err = c.GetEnsText(ctx, r.Name, *r.Key, opt)
					}
					if err != nil {
						return fmt.Errorf("%s lookup: %w", r.Name, err)
					}
					if got == nil && r.Want == nil {
						return nil
					}
					if got != nil && r.Want != nil && *got == *r.Want {
						return nil
					}
					return fmt.Errorf("%s record mismatch: got %v, want %v", r.Name, ensTestValue(got), ensTestValue(r.Want))
				}
				for _, r := range fixture.Records {
					if err := check(r, ENSNameOptions{}); err != nil {
						return err
					}
				}
				atTwo := ENSNameOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(2)}}
				if err := check(fixture.Replacement, atTwo); err != nil {
					return err
				}
				if err := check(fixture.Replacement, ENSNameOptions{BlockOptions: BlockOptions{Cache: "immutable"}}); err != nil {
					return err
				}
				if err := check(ensVector{Name: "missing.eth"}, ENSNameOptions{}); err != nil {
					return err
				}
				strict := ENSNameOptions{Strict: true}
				var rerr *rpc.Error
				if _, err := c.GetEnsText(ctx, "missing.eth", "url", strict); !errors.As(err, &rerr) {
					return fmt.Errorf("strict missing text: %v", err)
				}
				if err := check(ensVector{Name: "alice.other"}, strict); err != nil {
					return err
				}
				beforeDeployment := ENSNameOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(0)}}
				if _, err := c.GetEnsAddress(ctx, "alice.eth", beforeDeployment); err == nil || !strings.Contains(err.Error(), "deployed at block") {
					return fmt.Errorf("pre-deployment lookup: %v", err)
				}
				address := fixture.Records[0]
				if err := check(address, ENSNameOptions{UniversalResolverAddress: fixture.Resolver}); err != nil {
					return err
				}
				if err := clients.WaitCacheWrites(ctx); err != nil {
					return err
				}
				var reverted bool
				net.call("evm_revert", []any{snapshot}, &reverted)
				if !reverted {
					return fmt.Errorf("snapshot was not restored")
				}
				net.call("anvil_dropAllTransactions", []any{}, &ignored)
				net.mine()
				return check(address, atTwo)
			})
		})
	}
}

func ensTestValue(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func TestENSRecordErrors(t *testing.T) {
	const resolver = "0x000000000000000000000000000000000000c011"
	chain := Object{"name": "chain", "id": 1}
	transportErr := errors.New("resolver transport unavailable")
	tr := admissionRequester(func(context.Context, string, any, any) error { return transportErr })
	clients, err := NewClientSet([]Object{chain}, []rpc.Requester{tr})
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close(t.Context())
	c, err := clients.GetClient(chain)
	if err != nil {
		t.Fatal(err)
	}
	opt := ENSNameOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(1)}, UniversalResolverAddress: resolver}
	if _, err := c.GetEnsAddress(t.Context(), "alice.eth", opt); !errors.Is(err, transportErr) {
		t.Fatalf("ordinary transport error: %v", err)
	}
	for _, invalid := range []*big.Int{big.NewInt(-1), new(big.Int).Lsh(big.NewInt(1), 256)} {
		opt.CoinType = invalid
		if _, err := c.GetEnsAddress(t.Context(), "alice.eth", opt); err == nil || !strings.Contains(err.Error(), "coin type") {
			t.Fatal(err)
		}
	}
	if _, err := c.GetEnsText(t.Context(), "alice.eth", "url", opt, opt); err == nil {
		t.Fatal("accepted two options")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.GetEnsText(ctx, "alice.eth", "url", opt); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
}
