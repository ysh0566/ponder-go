package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ponder.local/ponder-go/filter"
)

type SourceChildAddress struct {
	Address     string
	BlockNumber uint64
}
type SourceFactoryChildren struct {
	FragmentID string
	Children   []SourceChildAddress
}

// Unbound inserts retain the source's two distinct commit stages and earliest
// block conflict rule; an explicit WithTransaction scope joins both stages.
// Factory fragment IDs already include source chain/range identity.
func (s *SourceSyncStore) InsertChildAddresses(ctx context.Context, chain uint64, children []SourceFactoryChildren) error {
	if err := s.scopeActive(ctx); err != nil {
		return err
	}
	if len(children) == 0 {
		return nil
	}
	factoryIDs := map[string]any{}
	slots := []string{}
	args := []any{}
	for _, item := range children {
		args = append(args, item.FragmentID)
		slots = append(slots, "($"+strconv.Itoa(len(args))+")")
	}
	if e := s.transaction(ctx, func(tx Transaction) error {
		rows, e := tx.Query(ctx, "INSERT INTO "+s.cache.table("factories")+"(fragment_id) VALUES"+strings.Join(slots, ",")+" ON CONFLICT(fragment_id) DO UPDATE SET fragment_id=excluded.fragment_id RETURNING id,fragment_id", args...)
		if e != nil {
			return e
		}
		for _, row := range rows.Rows {
			factoryIDs[fmt.Sprint(row["fragment_id"])] = row["id"]
		}
		return nil
	}); e != nil {
		return e
	}
	type childRow struct {
		factory any
		address string
		block   uint64
	}
	rows := []childRow{}
	for _, item := range children {
		for _, child := range item.Children {
			rows = append(rows, childRow{factoryIDs[item.FragmentID], child.Address, child.BlockNumber})
		}
	}
	size := s.maxQueryParameters / 4
	for offset := 0; offset < len(rows); offset += size {
		end := min(offset+size, len(rows))
		slots := []string{}
		args := []any{}
		for _, r := range rows[offset:end] {
			n := len(args)
			slots = append(slots, fmt.Sprintf("($%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4))
			args = append(args, r.factory, chain, r.block, r.address)
		}
		if e := s.transaction(ctx, func(tx Transaction) error {
			return tx.Exec(ctx, "INSERT INTO "+s.cache.table("factory_addresses")+" AS factory_addresses(factory_id,chain_id,block_number,address) VALUES"+strings.Join(slots, ",")+" ON CONFLICT(factory_id,address) DO UPDATE SET block_number=excluded.block_number WHERE excluded.block_number < factory_addresses.block_number", args...)
		}); e != nil {
			return e
		}
	}
	return nil
}

// SourceAddressMap is insertion ordered, just as the source Map of addresses.
type SourceAddressMap struct {
	Entries []SourceChildAddress
	indices map[string]int
}

func (m *SourceAddressMap) Len() int { return len(m.Entries) }
func (m *SourceAddressMap) Get(address string) (uint64, bool) {
	i, ok := m.indices[address]
	if !ok {
		return 0, false
	}
	return m.Entries[i].BlockNumber, true
}
func (s *SourceSyncStore) GetChildAddresses(ctx context.Context, factory filter.Object) (*SourceAddressMap, error) {
	if err := s.scopeActive(ctx); err != nil {
		return nil, err
	}
	ids := filter.FactoryFragmentIDs(factory)
	out := &SourceAddressMap{Entries: []SourceChildAddress{}, indices: map[string]int{}}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{}
	slots := []string{}
	for _, id := range ids {
		args = append(args, id)
		slots = append(slots, "$"+strconv.Itoa(len(args)))
	}
	e := s.transaction(ctx, func(tx Transaction) error {
		rows, e := tx.Query(ctx, "SELECT a.address,a.block_number::text AS block_number FROM "+s.cache.table("factory_addresses")+" a INNER JOIN "+s.cache.table("factories")+" f ON a.factory_id=f.id WHERE f.fragment_id IN ("+strings.Join(slots, ",")+")", args...)
		if e != nil {
			return e
		}
		for _, row := range rows.Rows {
			address := fmt.Sprint(row["address"])
			block, e := strconv.ParseUint(fmt.Sprint(row["block_number"]), 10, 64)
			if e != nil {
				return e
			}
			if i, ok := out.indices[address]; ok {
				out.Entries[i].BlockNumber = min(out.Entries[i].BlockNumber, block)
			} else {
				out.indices[address] = len(out.Entries)
				out.Entries = append(out.Entries, SourceChildAddress{address, block})
			}
		}
		return nil
	})
	return out, e
}
