package indexing

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestCallbackENSResolverAtEvent(t *testing.T) {
	const universal = "0x000000000000000000000000000000000000c011"
	// keccak256 of viem's findResolver calldata for alice.eth.
	const slot = "0xd5556a3b451f79d5505ea4fe265120adcae695c78718efc6bef22dadd99a9b41"
	first := common.HexToAddress("0x111122223333444455556666777788889999aaaa").Hex()
	second := common.HexToAddress("0x22223333444455556666777788889999aaaabbbb").Hex()
	// A 64-byte write sets [key] = value. A lookup loads the slot indexed
	// by keccak256(calldata) and returns (address, bytes32(0), uint256(0)).
	const code = "0x36604014602457" + // jump to the writer at 0x24 for a 64-byte input
		"3660006000373660002054600052" + // store the selected resolver in output word zero
		"6000602052600060405260606000f3" + // clear the other two output words and return
		"5b6020356000355500" // write the supplied value to the supplied key
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			net := newSourceClientChain(t, sourceChainContracts{})
			var ignored any
			net.call("anvil_setCode", []any{universal, code}, &ignored)
			write := func(address string) {
				net.send(universal, slot+strings.Repeat("0", 24)+address[2:])
			}
			write(first)
			var snapshot string
			net.call("evm_snapshot", []any{}, &snapshot)
			write(second)

			chain := Object{"name": "local", "id": 1, "cacheRpcRequests": true, "viemChain": Object{
				"ensTlds": []any{".eth"}, "contracts": Object{"ensUniversalResolver": Object{"address": universal, "blockCreated": 1}}}}
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{net.rpc}, adapterStore(t, dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			runClientCallback(t, ctx, dialect, chain, clients, func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				check := func(name, want string, opt ...ENSResolverOptions) error {
					value, err := c.GetEnsResolver(ctx, name, opt...)
					if err != nil || value != want {
						return fmt.Errorf("resolver for %q: %q, want %q: %v", name, value, want, err)
					}
					return nil
				}
				if err := check("alice.eth", first); err != nil {
					return err
				}
				atTwo := ENSResolverOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(2)}}
				if err := check("alice.eth", second, atTwo); err != nil {
					return err
				}
				if err := check("absent.eth", common.Address{}.Hex()); err != nil {
					return err
				}
				if _, err := c.GetEnsResolver(ctx, "alice.test"); err == nil {
					return fmt.Errorf("unconfigured ENS suffix accepted")
				}
				if _, err := c.GetEnsResolver(ctx, "alice.eth", ENSResolverOptions{BlockOptions: BlockOptions{BlockNumber: big.NewInt(0)}}); err == nil || !strings.Contains(err.Error(), "deployed at block") {
					return fmt.Errorf("pre-deployment lookup: %v", err)
				}
				immutable := ENSResolverOptions{BlockOptions: BlockOptions{Cache: "immutable"}}
				if err := check("alice.eth", second, immutable); err != nil {
					return err
				}
				if err := clients.WaitCacheWrites(ctx); err != nil {
					return err
				}
				var reverted bool
				if err := net.rpc.Call(ctx, "evm_revert", []any{snapshot}, &reverted); err != nil || !reverted {
					return fmt.Errorf("revert: %v, %v", reverted, err)
				}
				if err := net.rpc.Call(ctx, "anvil_dropAllTransactions", []any{}, &ignored); err != nil {
					return err
				}
				if err := net.rpc.Call(ctx, "evm_mine", []any{}, &ignored); err != nil {
					return err
				}
				if err := check("alice.eth", first, atTwo); err != nil {
					return err
				}
				if err := check("alice.eth", second, immutable); err != nil {
					return err
				}
				return check("alice.eth", first, ENSResolverOptions{UniversalResolverAddress: universal})
			})
		})
	}
}

func TestENSDNSNamePackets(t *testing.T) {
	// From the pinned viem packetToBytes implementation.
	for name, want := range map[string]string{
		"":                                "0x00",
		".":                               "0x00",
		".alice.eth.":                     "0x05616c6963650365746800",
		"é.eth":                           "0x02c3a90365746800",
		strings.Repeat("a", 256) + ".eth": "0x425b316461613730333461646162363664396563396530336532633839323031623833613734393765383564633562393731616139646165326363626237613230385d0365746800",
	} {
		if got := ensDNSPacket(name); got != want {
			t.Fatalf("%q = %s, want %s", name, got, want)
		}
	}
	for _, test := range []struct {
		input any
		name  string
		ok    bool
	}{{nil, "any.test", true}, {[]string(nil), "any.test", true}, {[]any(nil), "any.test", true}, {[]string{}, "alice.eth", false}, {[]any{}, "alice.eth", false}, {[]string{".eth"}, "alice.test", false}, {[]any{".eth"}, "alice.eth", true}} {
		c := Client{ensTLDs: ensSuffixes(test.input)}
		if c.allowsENSName(test.name) != test.ok {
			t.Fatalf("%v, %s", test.input, test.name)
		}
	}
}
