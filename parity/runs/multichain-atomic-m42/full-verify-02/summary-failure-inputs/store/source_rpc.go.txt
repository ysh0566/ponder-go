package store

import (
	"context"
	"ponder.local/ponder-go/rpc"
)

func (s *SourceSyncStore) InsertRPCRequestResults(ctx context.Context, chain uint64, requests []SourceRPCResult) error {
	if err := s.scopeActive(ctx); err != nil {
		return err
	}
	raw := make([]SourceRPCRawResult, len(requests))
	for i, r := range requests {
		value, err := rpc.SourceRequestValue(r.Request)
		if err != nil {
			return err
		}
		raw[i] = SourceRPCRawResult{value, r.BlockNumber, r.Result}
	}
	return s.InsertRPCRawRequestResults(ctx, chain, raw)
}
func (s *SourceSyncStore) InsertRPCRawRequestResults(ctx context.Context, chain uint64, requests []SourceRPCRawResult) error {
	if err := s.scopeActive(ctx); err != nil {
		return err
	}
	if len(requests) == 0 {
		return nil
	}
	return s.transaction(ctx, func(tx Transaction) error { return s.cache.insertSourceRPC(ctx, tx, chain, requests) })
}
func (s *SourceSyncStore) GetRPCRequestResults(ctx context.Context, chain uint64, requests []rpc.SourceRequest) ([]*string, error) {
	if err := s.scopeActive(ctx); err != nil {
		return nil, err
	}
	raw := make([]any, len(requests))
	for i, r := range requests {
		value, err := rpc.SourceRequestValue(r)
		if err != nil {
			return nil, err
		}
		raw[i] = value
	}
	return s.GetRPCRawRequestResults(ctx, chain, raw)
}
func (s *SourceSyncStore) GetRPCRawRequestResults(ctx context.Context, chain uint64, requests []any) ([]*string, error) {
	if err := s.scopeActive(ctx); err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return []*string{}, nil
	}
	var out []*string
	err := s.transaction(ctx, func(tx Transaction) error {
		var err error
		out, err = s.cache.getSourceRPC(ctx, tx, chain, requests)
		return err
	})
	return out, err
}
func (s *SourceSyncStore) PruneRPCRequestResults(ctx context.Context, chain uint64, blocks []uint64) error {
	if err := s.scopeActive(ctx); err != nil {
		return err
	}
	if len(blocks) == 0 {
		return nil
	}
	return s.transaction(ctx, func(tx Transaction) error { return s.cache.pruneSourceRPC(ctx, tx, chain, blocks) })
}
func (s *SourceSyncStore) RPCRequestCache(chain uint64) *SourceRPCRequestCache {
	if s.bound == nil {
		return s.cache.SourceRPCRequestCache(chain)
	}
	return &SourceRPCRequestCache{cache: s.cache, source: s, chain: chain}
}
