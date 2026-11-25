// The EIP-3668 lookup and the ENS local batch gateway follow viem 2.35.0.
// Its MIT license is preserved in licenses/npm/viem@2.35.0.
package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ysh0566/ponder-go/rpc"
)

// CCIPReadOptions controls EIP-3668 for a high-level contract or ENS read.
// The zero value uses the public gateway transport. HTTPClient permits a
// caller-supplied transport, for instance one restricted to a trusted gateway.
type CCIPReadOptions struct {
	Disabled   bool
	HTTPClient *http.Client
}

const offchainLookupSelector = "0x556f1830"
const ccipBatchURL = "x-batch-gateway:true"
const maxCCIPBytes = 4 << 20

var ccipABI = sync.OnceValues(func() (ethabi.ABI, error) {
	return ethabi.JSON(strings.NewReader(`[
	{"type":"error","name":"OffchainLookup","inputs":[{"type":"address"},{"type":"string[]"},{"type":"bytes"},{"type":"bytes4"},{"type":"bytes"}]},
	{"type":"function","name":"callback","inputs":[{"type":"bytes"},{"type":"bytes"}],"outputs":[]},
	{"type":"function","name":"query","inputs":[{"name":"queries","type":"tuple[]","components":[{"name":"sender","type":"address"},{"name":"urls","type":"string[]"},{"name":"data","type":"bytes"}]}],"outputs":[{"type":"bool[]"},{"type":"bytes[]"}]},
	{"type":"error","name":"HttpError","inputs":[{"type":"uint16"},{"type":"string"}]},
	{"type":"error","name":"Error","inputs":[{"type":"string"}]}
	]`))
})

func (c *Client) readEVM(ctx context.Context, tx Object, params []any, cache bool, opt CCIPReadOptions) (json.RawMessage, error) {
	ctx, finish, err := c.set.managedRoots.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	var raw json.RawMessage
	if cache {
		raw, err = c.request(ctx, "eth_call", params)
	} else {
		raw, err = c.rpcRequest(ctx, "eth_call", params, false)
	}
	if err == nil || opt.Disabled {
		return raw, err
	}
	return c.resolveCCIP(ctx, tx["to"], err, opt)
}

func ccipRevert(err error) string {
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) {
		return ""
	}
	var data string
	if json.Unmarshal(rpcErr.Data, &data) != nil {
		var nested struct{ Data string }
		if json.Unmarshal(rpcErr.Data, &nested) != nil {
			return ""
		}
		data = nested.Data
	}
	return data
}

func (c *Client) resolveCCIP(ctx context.Context, to any, rpcErr error, opt CCIPReadOptions) (json.RawMessage, error) {
	address, ok := contractAddress(to)
	if !ok || opt.Disabled {
		return nil, rpcErr
	}
	s := ccipSession{http: opt.HTTPClient, active: make(chan struct{}, 4)}
	if s.http == nil {
		s.http = publicGatewayClient
	}
	for depth := 0; ; depth++ {
		data := ccipRevert(rpcErr)
		if len(data) < 10 || !strings.EqualFold(data[:10], offchainLookupSelector) {
			return nil, rpcErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if depth >= 8 {
			return nil, fmt.Errorf("EIP-3668 callback limit exceeded")
		}
		if len(data) > 2+2*maxCCIPBytes {
			return nil, fmt.Errorf("OffchainLookup exceeds byte limit")
		}
		input, err := hexutil.Decode(data)
		if err != nil {
			return nil, fmt.Errorf("invalid OffchainLookup data: %w", err)
		}
		abi, err := ccipABI()
		if err != nil {
			return nil, err
		}
		values, err := abi.Errors["OffchainLookup"].Inputs.Unpack(input[4:])
		if err != nil {
			return nil, fmt.Errorf("invalid OffchainLookup data: %w", err)
		}
		sender := values[0].(common.Address)
		if sender != address {
			return nil, fmt.Errorf("OffchainLookup sender %s differs from target %s", sender, address)
		}
		result, err := s.query(ctx, ccipQuery{Sender: sender, Urls: values[1].([]string), Data: values[2].([]byte)}, 0)
		if err != nil {
			return nil, fmt.Errorf("EIP-3668 gateway: %w", err)
		}
		args, err := abi.Methods["callback"].Inputs.Pack(result, values[4])
		if err != nil {
			return nil, err
		}
		selector := values[3].([4]byte)
		tx := Object{"to": address.Hex(), "data": hexutil.Encode(append(selector[:], args...))}
		// The pinned viem call forwards only the sender and revert data to its
		// lookup helper. Its verification callback therefore executes at latest.
		var raw json.RawMessage
		raw, rpcErr = c.request(ctx, "eth_call", []any{tx, "latest"})
		if rpcErr == nil {
			return raw, nil
		}
	}
}

type ccipSession struct {
	http      *http.Client
	requests  atomic.Int32
	queries   atomic.Int32
	bodyBytes atomic.Int64
	active    chan struct{}
}
type ccipQuery struct {
	Sender common.Address
	Urls   []string
	Data   []byte
}

type ccipGatewayError struct {
	status  int
	message string
	cause   error
}

func (e *ccipGatewayError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s %v", e.message, e.cause)
	}
	if e.status != 0 {
		return fmt.Sprintf("%s HTTP %d", e.message, e.status)
	}
	return e.message
}
func (e *ccipGatewayError) Unwrap() error { return e.cause }

func (s *ccipSession) query(ctx context.Context, q ccipQuery, depth int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth >= 8 {
		return nil, fmt.Errorf("ENS batch nesting limit exceeded")
	}
	if slices.Contains(q.Urls, ccipBatchURL) {
		return s.batch(ctx, q.Data, depth)
	}
	var last error = errors.New("An unknown error occurred.")
	for _, tmpl := range q.Urls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s.requests.Add(1) > 64 {
			return nil, fmt.Errorf("EIP-3668 gateway request limit exceeded")
		}
		data, err := s.fetch(ctx, q, tmpl)
		if err == nil {
			return data, nil
		}
		last = err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, last
}

func (s *ccipSession) fetch(ctx context.Context, q ccipQuery, tmpl string) ([]byte, error) {
	data := hexutil.Encode(q.Data)
	uri := strings.Replace(tmpl, "{sender}", strings.ToLower(q.Sender.Hex()), 1)
	method := http.MethodPost
	var body io.Reader
	if strings.Contains(tmpl, "{data}") {
		method = http.MethodGet
		uri = strings.Replace(uri, "{data}", data, 1)
	} else {
		payload, err := json.Marshal(struct {
			Data   string `json:"data"`
			Sender string `json:"sender"`
		}{data, q.Sender.Hex()})
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, uri, body)
	if err != nil || (req.URL.Scheme != "https" && req.URL.Scheme != "http") || req.URL.Host == "" || req.URL.User != nil {
		return nil, &ccipGatewayError{message: "HTTP request failed.", cause: errors.New("invalid gateway URL")}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	select {
	case s.active <- struct{}{}:
		defer func() { <-s.active }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.bodyBytes.Load() > maxCCIPBytes {
		return nil, fmt.Errorf("EIP-3668 gateway byte limit exceeded")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, &ccipGatewayError{message: "HTTP request failed.", cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ccipGatewayError{status: resp.StatusCode, message: "HTTP request failed."}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCCIPBytes+1))
	if err != nil || s.bodyBytes.Add(int64(len(raw))) > maxCCIPBytes {
		return nil, &ccipGatewayError{message: "HTTP request failed.", cause: errors.New("unreadable or oversized gateway response")}
	}
	result := string(raw)
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		var obj struct{ Data json.RawMessage }
		if err = json.Unmarshal(raw, &obj); err != nil {
			return nil, &ccipGatewayError{message: "HTTP request failed.", cause: err}
		}
		result = ""
		_ = json.Unmarshal(obj.Data, &result)
	}
	decoded, err := hexutil.Decode(result)
	if err != nil {
		return nil, &ccipGatewayError{message: "Offchain gateway response is malformed. Response data must be a hex value."}
	}
	return decoded, nil
}

func (s *ccipSession) batch(ctx context.Context, data []byte, depth int) ([]byte, error) {
	abi, err := ccipABI()
	if err != nil {
		return nil, err
	}
	method := abi.Methods["query"]
	if len(data) > maxCCIPBytes {
		return nil, fmt.Errorf("ENS batch exceeds byte limit")
	}
	if len(data) < 4 || !bytes.Equal(data[:4], method.ID) {
		return nil, fmt.Errorf("invalid ENS batch query selector")
	}
	values, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		return nil, err
	}
	var queries []ccipQuery
	if err = method.Inputs.Copy(&queries, values); err != nil {
		return nil, err
	}
	if len(queries) > 64 || s.queries.Add(int32(len(queries))) > 64 {
		return nil, fmt.Errorf("ENS batch query limit exceeded")
	}
	responses := make([][]byte, len(queries))
	failures := make([]bool, len(queries))
	var group sync.WaitGroup
	for i, q := range queries {
		group.Go(func() {
			var queryErr error
			responses[i], queryErr = s.query(ctx, q, depth+1)
			if queryErr != nil {
				failures[i] = true
				responses[i] = ccipBatchError(abi, queryErr)
			}
		})
	}
	group.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return method.Outputs.Pack(failures, responses)
}

func ccipBatchError(abi ethabi.ABI, cause error) []byte {
	revert := abi.Errors["Error"]
	args := []any{cause.Error()}
	var gw *ccipGatewayError
	if errors.As(cause, &gw) {
		args = []any{gw.message}
		if gw.status != 0 {
			revert = abi.Errors["HttpError"]
			args = []any{uint16(gw.status), gw.message}
		}
	}
	encoded, _ := revert.Inputs.Pack(args...)
	return append(revert.ID[:4:4], encoded...)
}
