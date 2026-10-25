package rpc

import (
	"context"
	"fmt"
	"reflect"
)

// SourceRequest is a closed typed request family. The dynamic Call API and
// source raw storage adapter remain explicit escape hatches for runtime inputs.
// This initial typed family covers eth_call; other source method types remain.
type SourceRequest interface{ sourceRequest() (string, any) }

type SourceCallParameters struct {
	Transaction map[string]any
	Block       *string
}
type SourceCallRequest struct{ Params SourceCallParameters }

func (r SourceCallRequest) sourceRequest() (string, any) {
	params := []any{r.Params.Transaction}
	if r.Params.Block != nil {
		params = append(params, *r.Params.Block)
	}
	return "eth_call", params
}

// SourceRequestValue carries the actual typed request into the shared raw
// storage path. This also prevents a caller from rewriting the method tag.
func SourceRequestValue(request SourceRequest) (map[string]any, error) {
	method, params, err := sourceRequestParameters(request)
	if err != nil {
		return nil, err
	}
	return map[string]any{"method": method, "params": params}, nil
}
func sourceRequestParameters(request SourceRequest) (string, any, error) {
	if request == nil {
		return "", nil, fmt.Errorf("source request is required")
	}
	v := reflect.ValueOf(request)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return "", nil, fmt.Errorf("source request is required")
	}
	method, params := request.sourceRequest()
	return method, params, nil
}

// CallSource executes a typed source request through the same real transport.
func (c *Client) CallSource(ctx context.Context, request SourceRequest, out any) error {
	method, params, err := sourceRequestParameters(request)
	if err != nil {
		return err
	}
	return c.Call(ctx, method, params, out)
}

// CallSourceCached requires the same immutable/finalized guarantee as CallCached.
func (c *Client) CallSourceCached(ctx context.Context, request SourceRequest, out any) error {
	method, params, err := sourceRequestParameters(request)
	if err != nil {
		return err
	}
	return c.CallCached(ctx, method, params, out)
}
