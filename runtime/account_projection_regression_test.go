package runtime

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/rpc"
)

// A raw block's supplied JSON integer must survive account projection exactly,
// along with the hexadecimal control and the unchanged raw handler input.
func TestAccountProjectionPreservesLargeBlockQuantity(t *testing.T) {
	for _, quantity := range []string{`"0x20000000000001"`, `9007199254740993`} {
		t.Run(quantity, func(t *testing.T) {
			tx := map[string]any{"hash": "tx1", "blockHash": "block1", "blockNumber": "0x1", "transactionIndex": "0x0", "from": "0x0000000000000000000000000000000000000001", "to": "0x0000000000000000000000000000000000000002", "gas": "0x0", "gasPrice": "0x0", "value": "0x0", "nonce": "0x0", "v": "0x0", "r": "0x0", "s": "0x0", "type": "0x0", "input": "0x"}
			receipt := map[string]any{"transactionHash": "tx1", "transactionIndex": "0x0", "blockHash": "block1", "blockNumber": "0x1", "from": tx["from"], "to": tx["to"], "cumulativeGasUsed": "0x0", "effectiveGasPrice": "0x0", "gasUsed": "0x0", "logsBloom": "0x", "status": "0x1", "type": "0x0", "contractAddress": nil}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					ID     any
					Method string
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				if q.Method != "eth_getTransactionReceipt" {
					t.Error("unexpected RPC", q.Method)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": receipt})
			}))
			defer server.Close()
			var block Block
			data := `{"number":"0x1","hash":"block1","parentHash":"block0","timestamp":"0x2","difficulty":` + quantity + `,"gasLimit":"0x0","gasUsed":"0x0","size":"0x0","transactions":[]}`
			if err := json.Unmarshal([]byte(data), &block); err != nil {
				t.Fatal(err)
			}
			block.Transactions = []map[string]any{tx}
			before, err := json.Marshal(block)
			if err != nil {
				t.Fatal(err)
			}
			client := rpc.New(server.URL)
			client.Retries = 0
			client.RequestsPerSecond = 0
			engine := Engine{chain: config.Chain{Name: "local", ID: 1}, Client: client, Config: config.Config{Accounts: []config.AccountSource{{Name: "A", Chain: "local"}}}, Handlers: map[string]Handler{"A:transaction:to": func(context.Context, *Context) error { return nil }}}
			pending, err := engine.accountEvents(context.Background(), block, 1)
			if err != nil || len(pending) != 1 {
				t.Fatal("original native account path", pending, err)
			}
			value, ok := pending[0].event.Normalized.Block["difficulty"].(*big.Int)
			if !ok || value.String() != "9007199254740993" {
				t.Fatal("account projection rounded supplied block integer", pending[0].event.Normalized.Block["difficulty"])
			}
			after, err := json.Marshal(block)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("projection changed raw handler block", err)
			}
		})
	}
}
