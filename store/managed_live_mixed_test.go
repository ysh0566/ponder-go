package store

import (
	"errors"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
)

func TestMixedOmnichainRoles(t *testing.T) {
	m, c, scans := logOnlyScanFixture(t)
	m.opts.Chains = append(m.opts.Chains, MigrationChain{ID: 2})
	m.opts.EndBlocks[2], m.opts.runtimeStarts[2] = 3, 1
	filters := m.opts.runtimeLogFilters[1]
	for _, tc := range []struct {
		name  string
		input map[uint64][]filter.Object
		valid bool
	}{
		{"mixed", map[uint64][]filter.Object{1: filters, 2: nil}, true},
		{"missing-role", map[uint64][]filter.Object{1: filters}, false},
		{"unknown-role", map[uint64][]filter.Object{1: filters, 3: nil}, false},
		{"empty-filter-list", map[uint64][]filter.Object{1: filters, 2: {}}, false},
		{"no-log-role", map[uint64][]filter.Object{1: nil, 2: nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeLogOnlyFilters(tc.input, m.opts)
			if (err == nil) != tc.valid || tc.valid && (len(got) != 1 || got[1] == nil) {
				t.Fatalf("filters %v, error %v", got, err)
			}
		})
	}
	c.Version = m.version()
	if c.Version != managedLiveMixedVersion {
		t.Fatal(c.Version)
	}
	for _, id := range []uint64{1, 2} {
		cp := checkpoint.Block(101, id, 1, true)
		cp.EventType, cp.EventIndex = checkpoint.Blocks, 0
		_, closing := optsPosition(checkpoint.MustEncode(cp), m.opts)
		cp.TransactionIndex, cp.EventType = 0, checkpoint.Logs
		_, log := optsPosition(checkpoint.MustEncode(cp), m.opts)
		if closing != (id == 2) || log != (id == 1) {
			t.Fatalf("chain %d accepts closing=%t log=%t", id, closing, log)
		}
	}
	ends, err := m.validateLogOnlyScan(c, scans)
	if err != nil || len(ends) != 1 || ends[1] != scans[1].Scanned.Last {
		t.Fatalf("scan heads %v error %v", ends, err)
	}
	scans[2] = scans[1]
	delete(scans, 1)
	if _, err = m.validateLogOnlyScan(c, scans); !errors.Is(err, ErrManagedLiveBatchConflict) {
		t.Fatalf("closing chain substituted for log chain: %v", err)
	}
}
