package store

import (
	"context"
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/rpc"
)

func runtimeCacheError(err error) <-chan error {
	done := make(chan error, 1)
	done <- err
	close(done)
	return done
}

// BeginRuntimeCachePut prepares and snapshots the native SQL payload before
// admission returns. The actual native transaction has one joined completion.
func (d *ManagedLiveDomains) BeginRuntimeCachePut(ctx context.Context, chain uint64, key string, value json.RawMessage) <-chan error {
	c := d.storage.syncCache
	adapter := c.SourceRPCRequestCache(chain)
	request, err := adapter.request(key)
	if err != nil {
		return runtimeCacheError(err)
	}
	if !json.Valid(value) {
		return runtimeCacheError(fmt.Errorf("RPC result must be JSON"))
	}
	block, err := sourceRequestBlock(request)
	if err != nil {
		return runtimeCacheError(err)
	}
	query, args, err := c.prepareSourceRPC(chain, []SourceRPCRawResult{{Request: request, BlockNumber: block, Result: string(value)}})
	if err != nil {
		return runtimeCacheError(err)
	}
	run, finish, err := d.admitRuntimeSync(ctx)
	if err != nil {
		return runtimeCacheError(err)
	}
	done := make(chan error, 1)
	go func() {
		defer close(done)
		defer finish()
		native := context.WithoutCancel(run)
		done <- c.transaction(native, func(tx Transaction) error {
			if err := run.Err(); err != nil {
				return err
			}
			if err := tx.Exec(native, query, args...); err != nil {
				return err
			}
			return run.Err()
		})
	}()
	return done
}
func (d *ManagedLiveDomains) BeginRuntimeCacheGet(ctx context.Context, chain uint64, key string) <-chan rpc.CacheLookupResult {
	done := make(chan rpc.CacheLookupResult, 1)
	fail := func(err error) <-chan rpc.CacheLookupResult {
		done <- rpc.CacheLookupResult{Err: err}
		close(done)
		return done
	}
	c := d.storage.syncCache
	request, err := c.SourceRPCRequestCache(chain).request(key)
	if err != nil {
		return fail(err)
	}
	if _, err = sourceRequestBlock(request); err != nil {
		return fail(err)
	}
	hash, err := SourceRPCRequestHash(request)
	if err != nil {
		return fail(err)
	}
	query := "SELECT result FROM " + c.table("rpc_request_results") + " WHERE chain_id=$1 AND request_hash=$2"
	run, finish, err := d.admitRuntimeSync(ctx)
	if err != nil {
		return fail(err)
	}
	go func() {
		defer close(done)
		defer finish()
		var out rpc.CacheLookupResult
		native := context.WithoutCancel(run)
		out.Err = c.transaction(native, func(tx Transaction) error {
			if err := run.Err(); err != nil {
				return err
			}
			rows, err := tx.Query(native, query, chain, hash)
			if err != nil {
				return err
			}
			if len(rows.Rows) > 0 {
				out.Hit = true
				out.Value = json.RawMessage(fmt.Sprint(rows.Rows[0]["result"]))
			}
			return run.Err()
		})
		done <- out
	}()
	return done
}
