package store

import (
	"context"
	"ponder.local/ponder-go/rpc"
)

func (s *SourceSyncStore) InsertRPCRequestResults(ctx context.Context, chain uint64, requests []SourceRPCResult) error {
	return s.cache.InsertSourceRPCRequestResults(ctx, chain, requests)
}
func (s *SourceSyncStore) InsertRPCRawRequestResults(ctx context.Context, chain uint64, requests []SourceRPCRawResult) error {
	return s.cache.InsertSourceRPCRawRequestResults(ctx, chain, requests)
}
func (s *SourceSyncStore) GetRPCRequestResults(ctx context.Context, chain uint64, requests []rpc.SourceRequest) ([]*string, error) {
	return s.cache.GetSourceRPCRequestResults(ctx, chain, requests)
}
func (s *SourceSyncStore) GetRPCRawRequestResults(ctx context.Context, chain uint64, requests []any) ([]*string, error) {
	return s.cache.GetSourceRPCRawRequestResults(ctx, chain, requests)
}
func (s *SourceSyncStore) PruneRPCRequestResults(ctx context.Context, chain uint64, blocks []uint64) error {
	return s.cache.PruneSourceRPCRequestResults(ctx, chain, blocks)
}
func (s *SourceSyncStore) RPCRequestCache(chain uint64) *SourceRPCRequestCache {
	return s.cache.SourceRPCRequestCache(chain)
}
