package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/metrics"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestApplicationHistoricalTimings(t *testing.T) {
	t.Setenv("PONDER_COMMAND", "")
	for _, order := range []schema.Ordering{schema.Multichain, schema.Isolated, schema.Omnichain} {
		for _, dialect := range []string{"postgres", "pglite"} {
			t.Run(string(order)+"/"+dialect, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				defer cancel()
				const genesis = 1700000000
				var clients []*rpc.Client
				window := 12
				cfg := config.Config{Ordering: order}
				mine := func(client *rpc.Client, offset int) {
					t.Helper()
					var result any
					if err := client.Call(ctx, "evm_setNextBlockTimestamp", []any{genesis + offset}, &result); err != nil {
						t.Fatal(err)
					}
					if err := client.Call(ctx, "evm_mine", []any{}, &result); err != nil {
						t.Fatal(err)
					}
				}
				for id, name := range []string{"a", "b"} {
					url := testanvil.StartChain(t, uint64(id+1), "--no-mining", "--timestamp", strconv.Itoa(genesis))
					client := rpc.New(url)
					clients = append(clients, client)
					for _, offset := range []int{10, 20, 30} {
						mine(client, offset)
					}
					cfg.Chains = append(cfg.Chains, config.Chain{Name: name, ID: uint64(id + 1), RPC: []string{url}, ReorgWindow: &window, PollingIntervalMS: 25})
					cfg.Blocks = append(cfg.Blocks, config.BlockSource{Name: "head", Chain: name, StartBlock: 1, Interval: 1})
				}
				b := stagedBackend(t, ctx, dialect, "memory://")
				defer b.Close()
				model := schema.Schema{Tables: []schema.Table{{
					Name: "entry", PrimaryKey: []string{"chain_id", "id"},
					Columns: []schema.Column{
						{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true},
						{Name: "id", Type: "text", NotNull: true},
					},
				}}}
				db, err := store.New(ctx, b, model, fmt.Sprintf("ponder_go_durations_%x", time.Now().UnixNano()), order)
				if err != nil {
					t.Fatal(err)
				}
				applied := make(chan uint64, 16)
				indexer, err := NewIndexer(cfg, db, map[string]Handler{"head:block": func(ctx context.Context, c *Context) error {
					n, err := Number(c.Event.Block.Number)
					if err != nil {
						return err
					}
					want := n == 1 && (order != schema.Omnichain || c.Chain.ID == 1)
					if (metrics.Historical(ctx) != nil) != want {
						return fmt.Errorf("unexpected timing context for chain %d block %d", c.Chain.ID, n)
					}
					if n == 4 {
						var version string
						if err := c.Client.Call(ctx, "web3_clientVersion", []any{}, &version); err != nil {
							return err
						}
						if err := c.Client.Call(ctx, "ponder_missingMethod", []any{}, &version); err == nil {
							return fmt.Errorf("node accepted an unknown RPC method")
						}
					}
					_, err = c.DB.Indexing().InsertOne(ctx, "entry", store.Row{"chainId": int64(c.Chain.ID), "id": c.Event.Block.Number}, store.Conflict{})
					if err == nil && n == 4 {
						applied <- c.Chain.ID
					}
					return err
				}})
				if err != nil {
					t.Fatal(err)
				}
				app, err := LaunchApplication(ctx, indexer, query.New(db), "127.0.0.1", 0)
				if err != nil {
					t.Fatal(err)
				}
				defer app.Shutdown(context.Background())
				select {
				case <-app.ready:
				case <-app.done:
					t.Fatalf("stopped before catchup: %v", app.Wait())
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				scrape := func() string {
					t.Helper()
					req, err := http.NewRequestWithContext(ctx, "GET", "http://"+app.Addr().String()+"/metrics", nil)
					if err != nil {
						t.Fatal(err)
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil || resp.StatusCode != http.StatusOK {
						t.Fatalf("scrape: %d, %v", resp.StatusCode, err)
					}
					return string(body)
				}
				historicalSamples := func(body string) string {
					var lines []string
					for _, line := range strings.Split(body, "\n") {
						if strings.HasPrefix(line, "ponder_historical_") {
							lines = append(lines, line)
						}
					}
					return strings.Join(lines, "\n")
				}
				firstScrape := scrape()
				settings := fmt.Sprintf(`ponder_settings_info{command="start",database="%s",ordering="%s"} 1`, dialect, order)
				if !strings.Contains(firstScrape, settings) {
					t.Fatalf("missing settings sample %s", settings)
				}
				if !strings.Contains(firstScrape, "ponder_indexing_function_duration_count") {
					t.Fatal("no callback timings at initial catch-up")
				}
				if strings.Contains(firstScrape, "ponder_realtime_latency_count") || strings.Contains(firstScrape, "ponder_realtime_block_arrival_latency_count") {
					t.Fatal("initial catch-up published realtime head latency")
				}
				for _, chain := range cfg.Chains {
					sample := fmt.Sprintf(`ponder_rpc_request_duration_count{chain="%s",method="eth_chainId"}`, chain.Name)
					if !strings.Contains(firstScrape, sample) {
						t.Fatalf("missing chain RPC sample %s", sample)
					}
				}
				first := historicalSamples(firstScrape)
				for _, series := range []string{
					`extract_duration{step="decode"}`,
					`transform_duration{step="begin"}`,
					`transform_duration{step="index"}`,
					`transform_duration{step="load"}`,
					`transform_duration{step="commit"}`,
				} {
					if !strings.Contains(first, series) {
						t.Fatalf("missing %s: %s", series, first)
					}
				}
				for _, c := range clients {
					mine(c, 40)
					mine(c, 50)
				}
				seen := map[uint64]bool{}
				for len(seen) < 2 {
					select {
					case id := <-applied:
						seen[id] = true
					case <-app.done:
						t.Fatalf("stopped during live indexing: %v", app.Wait())
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				liveScrape := scrape()
				for _, chain := range cfg.Chains {
					for _, sample := range []string{
						fmt.Sprintf(`ponder_rpc_request_duration_count{chain="%s",method="web3_clientVersion"} 1`, chain.Name),
						fmt.Sprintf(`ponder_rpc_request_error_total{chain="%s",method="ponder_missingMethod"} 1`, chain.Name),
					} {
						if !strings.Contains(liveScrape, sample+"\n") {
							t.Fatalf("missing live callback sample %s", sample)
						}
					}
				}
				if live := historicalSamples(liveScrape); live != first {
					t.Fatalf("live work changed historical timings:\n%s\n%s", first, live)
				}
				// Each tip must become eligible at the global watermark, including
				// chain B, whose ID sorts after A at an equal timestamp.
				for i, name := range []string{"b", "a"} {
					mine(clients[i], 60+10*i)
					for {
						body := scrape()
						label := fmt.Sprintf(`_count{chain=%q} `, name)
						callbackCount := fmt.Sprintf("ponder_indexing_function_duration_count{chain=%q,event=%q} ", name, "head:block")
						_, sample, _ := strings.Cut(body, callbackCount)
						value, _, _ := strings.Cut(sample, "\n")
						count, _ := strconv.Atoi(value)
						if count >= 4 && strings.Contains(body, "ponder_realtime_latency"+label) && strings.Contains(body, "ponder_realtime_block_arrival_latency"+label) {
							break
						}
						select {
						case <-app.done:
							t.Fatalf("waiting for chain %s live metrics: %v", name, app.Wait())
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						case <-time.After(25 * time.Millisecond):
						}
					}
				}
			})
		}
	}
}
