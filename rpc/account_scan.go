package rpc

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// AccountScanBlock is a complete block for an uncached account range proof.
// Receipts and trace roots correspond to the transactions in block order.
type AccountScanBlock struct {
	Block    map[string]any
	Receipts []map[string]any
	Traces   []CallTrace
}

// GetAccountScanBlock addresses every block-wide read by its finalized hash.
// A missing transaction or trace root must not certify an empty callback range.
func (a *Actions) GetAccountScanBlock(ctx context.Context, hash string, transfers bool, options *ActionOptions) (AccountScanBlock, error) {
	var data AccountScanBlock
	req := ActionRequest{"eth_getBlockByHash", []any{hash, true}}
	var raw map[string]any
	if err := a.requester.Call(ctx, req.Method, req.Params, &raw); err != nil {
		return data, err
	}
	if blockHash, ok := raw["hash"].(string); !ok || !strings.EqualFold(blockHash, hash) {
		return data, fmt.Errorf("account scan block does not match requested hash %s", hash)
	}
	txs, err := sourceRPCTransactions(raw)
	if err != nil {
		return data, err
	}
	hashes, err := accountScanTransactionHashes(txs, hash)
	if err != nil {
		return data, err
	}
	block, err := StandardizeSourceBlock(raw, req)
	if err != nil {
		return data, err
	}
	if err := ValidateSourceTransactionsAndBlock(block, req); err != nil {
		return data, err
	}
	var count string
	if err := a.requester.Call(ctx, "eth_getBlockTransactionCountByHash", []any{hash}, &count); err != nil {
		return data, err
	}
	n, err := hexutil.DecodeUint64(count)
	if err != nil || n != uint64(len(txs)) {
		return data, fmt.Errorf("incomplete transactions for account scan block %s", hash)
	}
	responses, err := a.GetTransactionReceipts(ctx, hash, hashes, options)
	if err != nil {
		return data, err
	}
	for _, res := range responses {
		if err := ValidateSourceReceiptsAndBlock(res.Receipts, block, res.Request, req); err != nil {
			return AccountScanBlock{}, err
		}
		for _, receipt := range res.Receipts {
			i := len(data.Receipts)
			if i >= len(hashes) || receipt["transactionHash"] != hashes[i] || (receipt["status"] != "0x0" && receipt["status"] != "0x1") {
				return AccountScanBlock{}, fmt.Errorf("invalid account scan receipt at index %d of %s", i, hash)
			}
			data.Receipts = append(data.Receipts, receipt)
		}
	}
	if len(data.Receipts) != len(txs) {
		return AccountScanBlock{}, fmt.Errorf("incomplete receipts for account scan block %s", hash)
	}
	if transfers && n > 0 {
		data.Traces, err = a.accountScanTraces(ctx, hash, txs, data.Receipts)
		if err != nil {
			return AccountScanBlock{}, err
		}
	}
	data.Block = block
	return data, nil
}

func accountScanAddress(address string) bool {
	return len(address) == 2+2*common.AddressLength && common.IsHexAddress(address)
}

func accountScanTransactionHashes(txs []map[string]any, hash string) ([]string, error) {
	hashes := make([]string, len(txs))
	seen := make(map[string]bool, len(txs))
	for i, tx := range txs {
		txHash, _ := tx["hash"].(string)
		bytes, e := hexutil.Decode(txHash)
		index, ie := rpcQuantity(tx["transactionIndex"])
		from, _ := tx["from"].(string)
		to, present := tx["to"]
		addr, _ := to.(string)
		if e != nil || len(bytes) != common.HashLength || seen[strings.ToLower(txHash)] || ie != nil || !index.IsUint64() || index.Uint64() != uint64(i) || !accountScanAddress(from) || !present || (to != nil && !accountScanAddress(addr)) {
			return nil, fmt.Errorf("invalid account scan transaction at index %d of %s", i, hash)
		}
		hashes[i], seen[strings.ToLower(txHash)] = txHash, true
	}
	return hashes, nil
}

func (a *Actions) accountScanTraces(ctx context.Context, hash string, txs, receipts []map[string]any) ([]CallTrace, error) {
	traceReq := ActionRequest{"debug_traceBlockByHash", []any{hash, map[string]any{"tracer": "callTracer"}}}
	var roots []map[string]any
	if err := a.requester.Call(ctx, traceReq.Method, traceReq.Params, &roots); err != nil {
		return nil, err
	}
	if len(roots) != len(txs) {
		return nil, fmt.Errorf("incomplete trace roots for account scan block %s", hash)
	}
	for i, entry := range roots {
		frame, ok := entry["result"].(map[string]any)
		if !ok || entry["txHash"] != txs[i]["hash"] {
			return nil, fmt.Errorf("invalid account scan trace root at index %d of %s", i, hash)
		}
		_, reverted := frame["error"]
		if !reverted {
			from, _ := frame["from"].(string)
			to, _ := frame["to"].(string)
			recipient, isCall := txs[i]["to"].(string)
			wantType := "CALL"
			if !isCall {
				recipient, _ = receipts[i]["contractAddress"].(string)
				wantType = "CREATE"
			}
			value, ve := rpcQuantity(frame["value"])
			amount, ae := rpcQuantity(txs[i]["value"])
			if frame["type"] != wantType || !strings.EqualFold(from, txs[i]["from"].(string)) || !strings.EqualFold(to, recipient) || ve != nil || ae != nil || value.Cmp(amount) != 0 {
				return nil, fmt.Errorf("trace root disagrees with transaction %s", txs[i]["hash"])
			}
		}
		if reverted != (receipts[i]["status"] == "0x0") {
			return nil, fmt.Errorf("account scan trace and receipt disagree for %s", txs[i]["hash"])
		}
	}
	return flattenSourceCallTraces(roots, traceReq, validateAccountFrame)
}

// A reverted frame suppresses its entire subtree, like FlattenSourceCallTraces.
// A successful frame must identify both endpoints and any transferable value.
func validateAccountFrame(frame map[string]any) error {
	if reason, exists := frame["error"]; exists {
		if msg, ok := reason.(string); ok && msg != "" {
			return nil
		}
		return fmt.Errorf("invalid account scan trace error")
	}
	// The source decoder treats false as an absent subtree. A scan requires
	// an actual list whenever the provider supplies child calls.
	switch frame["calls"].(type) {
	case nil, []any, []map[string]any:
	default:
		return fmt.Errorf("invalid account scan child traces")
	}
	from, _ := frame["from"].(string)
	to, _ := frame["to"].(string)
	kind, _ := frame["type"].(string)
	if !accountScanAddress(from) || !accountScanAddress(to) || kind == "" {
		return fmt.Errorf("invalid endpoints or type in account scan trace")
	}
	switch kind {
	case "CALL", "CREATE", "CREATE2", "SELFDESTRUCT":
		v, _ := frame["value"].(string)
		if _, err := hexutil.DecodeBig(v); err != nil {
			return fmt.Errorf("invalid account scan %s value: %w", kind, err)
		}
	case "STATICCALL", "DELEGATECALL", "CALLCODE":
	default:
		return fmt.Errorf("unknown account scan trace type %q", kind)
	}
	return nil
}
