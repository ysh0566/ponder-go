package indexing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/rpc"
)

func TestCallbackENSAvatarAtEvent(t *testing.T) {
	const dir = "../testdata/indexing/ens/"
	var f struct {
		Resolver, Token, ReplaceAvatar, ReplaceNFT, NewAvatar string
		Records                                               []ensVector
		TokenSetters                                          []string
		Assets                                                map[string]struct {
			Status     int
			Type, Body string
		}
	}
	raw, err := os.ReadFile(dir + "avatars.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := f.Assets[r.Method+" "+r.URL.RequestURI()]
		if !ok {
			t.Errorf("unexpected asset request %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", a.Type)
		if a.Status != 0 {
			w.WriteHeader(a.Status)
		}
		fmt.Fprint(w, a.Body)
	}))
	defer server.Close()
	opt := ENSAvatarOptions{HTTPClient: server.Client(), IPFSGateway: server.URL, ArweaveGateway: server.URL}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			chain := Object{"name": "local", "id": 1, "cacheRpcRequests": true, "viemChain": Object{"contracts": Object{"ensUniversalResolver": Object{"address": f.Resolver, "blockCreated": 1}}}}
			net := newSourceClientChain(t, sourceChainContracts{})
			var ignored any
			for addr, file := range map[string]string{f.Resolver: "RecordResolver.hex", f.Token: "AvatarToken.hex"} {
				code, err := os.ReadFile(dir + file)
				if err != nil {
					t.Fatal(err)
				}
				net.call("anvil_setCode", []any{addr, strings.TrimSpace(string(code))}, &ignored)
			}
			queue := func(to, data string) {
				net.call("eth_sendTransaction", []any{Object{"from": sourceClientAlice, "to": to, "data": data, "gas": "0x800000"}}, &ignored)
			}
			for _, record := range f.Records {
				queue(f.Resolver, record.Setter)
			}
			for _, setter := range f.TokenSetters {
				queue(f.Token, setter)
			}
			net.mine()
			var snap string
			net.call("evm_snapshot", []any{}, &snap)
			queue(f.Resolver, f.ReplaceAvatar)
			queue(f.Token, f.ReplaceNFT)
			net.mine()
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{net.rpc}, adapterStore(t, dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			runClientCallback(t, ctx, dialect, chain, clients, func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				check := func(name string, want *string, options ENSAvatarOptions) error {
					got, err := c.GetEnsAvatar(ctx, name, options)
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					if got == nil && want == nil {
						return nil
					}
					if got != nil && want != nil && *got == strings.Replace(*want, "@", server.URL, 1) {
						return nil
					}
					return fmt.Errorf("%s: got %v want %v", name, ensTestValue(got), ensTestValue(want))
				}
				for _, r := range f.Records {
					if err := check(r.Name, r.Want, opt); err != nil {
						return err
					}
				}
				if err := check("absent.eth", nil, opt); err != nil {
					return err
				}
				strict := opt
				strict.Strict = true
				var rpcErr *rpc.Error
				if _, err := c.GetEnsAvatar(ctx, "absent.eth", strict); !errors.As(err, &rpcErr) {
					return fmt.Errorf("strict missing record: %v", err)
				}
				if err := check("garbage.eth", nil, strict); err != nil {
					return err
				}
				if _, err := c.GetEnsAvatar(ctx, "erc721.eth", opt, opt); err == nil {
					return fmt.Errorf("accepted two option objects")
				}
				alt := opt
				alt.UniversalResolverAddress = f.Resolver
				alt.BlockNumber = big.NewInt(2)
				if err := check("inline.eth", &f.NewAvatar, alt); err != nil {
					return err
				}
				if err := clients.WaitCacheWrites(ctx); err != nil {
					return err
				}
				var reverted bool
				net.call("evm_revert", []any{snap}, &reverted)
				if !reverted {
					return fmt.Errorf("did not revert")
				}
				net.call("anvil_dropAllTransactions", []any{}, &ignored)
				net.mine()
				return check("inline.eth", f.Records[0].Want, alt)
			})
		})
	}
}

func TestAvatarGateway(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "192.168.1.7", "169.254.169.254", "fd00::1", "100.64.1.1", "64:ff9b::7f00:1"} {
		if publicGatewayIP(netip.MustParseAddr(ip)) {
			t.Fatalf("local gateway address accepted: %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicGatewayIP(netip.MustParseAddr(ip)) {
			t.Fatalf("public IP rejected: %s", ip)
		}
	}
	var calls atomic.Int32
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/wait" {
			close(started)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "image/gif")
	}))
	defer s.Close()
	if _, err := (ENSAvatarOptions{}).imageURI(t.Context(), s.URL); err == nil || calls.Load() != 0 {
		t.Fatalf("default local request: hits=%d error=%v", calls.Load(), err)
	}
	opt := ENSAvatarOptions{HTTPClient: s.Client()}
	if u, err := opt.imageURI(t.Context(), s.URL); err != nil || u != s.URL {
		t.Fatalf("explicit client: %s %v", u, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := opt.imageURI(ctx, s.URL+"/wait"); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, uri := range []string{"<svg>🌞</svg>", "missing", "http://user:password@example.test/avatar"} {
		if _, err := opt.imageURI(t.Context(), uri); err == nil {
			t.Fatalf("accepted %q", uri)
		}
	}
	inline := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>"))
	if u, err := opt.imageURI(t.Context(), "<svg/>"); err != nil || u != inline {
		t.Fatalf("svg %v %v", u, err)
	}
}
