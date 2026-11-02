package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ponder.local/ponder-go/checkpoint"
)

func liveTestOptions() (IndependentStorageOptions, ManagedLiveOptions) {
	o := IndependentStorageOptions{BusinessNamespace: "ponder_go_ml_unit", SyncNamespace: "ponder_go_ml_cache"}
	m := ManagedLiveOptions{ManagedFiniteOptions: ManagedFiniteOptions{BuildID: "m45-stage1", PlanID: "fixed-plan", Chains: []MigrationChain{{ID: 1, Name: "one"}, {ID: 10, Name: "ten"}}, EndBlocks: map[uint64]uint64{1: 2, 10: 9}}, OpenID: "open-1", FinalizedHeads: map[uint64]ManagedLiveHead{}}
	for _, id := range []uint64{1, 10} {
		m.FinalizedHeads[id] = ManagedLiveHead{Checkpoint: checkpoint.MustEncode(checkpoint.Block(100, id, 4, true)), Hash: "0x" + strings.Repeat("ab", 32), ParentHash: "0x" + strings.Repeat("cd", 32)}
	}
	return o, m
}

func TestManagedLiveOptions(t *testing.T) {
	o, in := liveTestOptions()
	got, err := normalizeManagedLiveOptions(o, in)
	if err != nil {
		t.Fatal(err)
	}
	live := &ManagedLive{opts: got, owner: &ManagedFinite{identity: "same-plan"}}
	if live.historyCuts()[1] != 2 || live.historyCuts()[10] != 4 || got.EndBlocks[10] != 9 {
		t.Fatal("historical cut was conflated with final end")
	}
	before := live.requestHash()
	delete(in.FinalizedHeads, 1)
	in.Chains[0].Name = "mutated"
	in.EndBlocks[10] = 0
	if live.requestHash() != before || got.Chains[0].Name != "one" || got.EndBlocks[10] != 9 {
		t.Fatal("caller mutation changed receipt inputs")
	}
	_, other := liveTestOptions()
	h := other.FinalizedHeads[1]
	h.Checkpoint = checkpoint.MustEncode(checkpoint.Block(101, 1, 5, true))
	other.FinalizedHeads[1] = h
	other, err = normalizeManagedLiveOptions(o, other)
	if err != nil {
		t.Fatal(err)
	}
	if (&ManagedLive{opts: other, owner: live.owner}).requestHash() == before {
		t.Fatal("changed fresh heads reused the same receipt hash")
	}
	for name, change := range map[string]func(*ManagedLiveOptions){
		"missing":     func(m *ManagedLiveOptions) { delete(m.FinalizedHeads, 10) },
		"extra":       func(m *ManagedLiveOptions) { m.FinalizedHeads[20] = m.FinalizedHeads[10] },
		"wrong-chain": func(m *ManagedLiveOptions) { m.FinalizedHeads[10] = m.FinalizedHeads[1] },
		"not-block-end": func(m *ManagedLiveOptions) {
			h := m.FinalizedHeads[1]
			h.Checkpoint = checkpoint.MustEncode(checkpoint.Block(100, 1, 4, false))
			m.FinalizedHeads[1] = h
		},
		"bad-hash": func(m *ManagedLiveOptions) { h := m.FinalizedHeads[1]; h.Hash = "0x1234"; m.FinalizedHeads[1] = h },
		"bad-parent": func(m *ManagedLiveOptions) {
			h := m.FinalizedHeads[1]
			h.ParentHash = "0x" + strings.Repeat("zz", 32)
			m.FinalizedHeads[1] = h
		},
		"missing-open-id": func(m *ManagedLiveOptions) { m.OpenID = "" },
		"control-open-id": func(m *ManagedLiveOptions) { m.OpenID = "one\nother" },
		"max-checkpoint": func(m *ManagedLiveOptions) {
			h := m.FinalizedHeads[1]
			h.Checkpoint = checkpoint.MaxString
			m.FinalizedHeads[1] = h
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, m := liveTestOptions()
			change(&m)
			if _, e := normalizeManagedLiveOptions(o, m); e == nil {
				t.Fatal("accepted malformed live snapshot")
			}
		})
	}
}

func TestManagedLiveFreshFinalizedTimestamp(t *testing.T) {
	o, in := liveTestOptions()
	m, e := normalizeManagedLiveOptions(o, in)
	if e != nil {
		t.Fatal(e)
	}
	states := []CheckpointState{}
	for _, c := range m.Chains {
		states = append(states, CheckpointState{ChainID: c.ID, ChainName: c.Name, LatestCheckpoint: checkpoint.MustEncode(checkpoint.Block(102, c.ID, 6, true)), FinalizedCheckpoint: checkpoint.MustEncode(checkpoint.Block(100, c.ID, 4, true)), SafeCheckpoint: checkpoint.MustEncode(checkpoint.Block(98, c.ID, 2, true))})
	}
	if e := validateLiveFinalizedHeads(m, states); e != nil {
		t.Fatal("finalized above global safe must remain valid:", e)
	}
	if e := validateLiveFinalizedHeads(m, states[:1]); e == nil {
		t.Fatal("accepted partial chain set")
	}
	h := m.FinalizedHeads[1]
	h.Checkpoint = checkpoint.MustEncode(checkpoint.Block(99, 1, 999, true))
	m.FinalizedHeads[1] = h
	if e := validateLiveFinalizedHeads(m, states); e == nil {
		t.Fatal("higher block number hid a timestamp regression")
	}
}

func TestManagedLiveLegacyMutationGuard(t *testing.T) {
	owner := &ManagedFinite{}
	live := &ManagedLive{owner: owner}
	s := &Store{staged: true, managedLive: live}
	a := &Actions{s}
	cp := checkpoint.Block(10, 1, 1, true)
	states := []CheckpointState{{ChainID: 1, ChainName: "one", LatestCheckpoint: checkpoint.MustEncode(cp), FinalizedCheckpoint: checkpoint.MustEncode(cp), SafeCheckpoint: checkpoint.MustEncode(cp)}}
	for _, closed := range []bool{false, true} {
		owner.closed.Store(closed)
		want := ErrManagedLiveLegacyMutation
		if closed {
			want = ErrManagedLiveClosed
		}
		for name, call := range map[string]func() error{
			"triggers":       func() error { return a.CreateTriggers(context.Background()) },
			"crash-recovery": func() error { return a.CrashRecovery(context.Background()) },
			"setup": func() error {
				return a.InitializeMultichain(context.Background(), states, func(*Writer, func(uint64) error) error { t.Fatal("callback admitted"); return nil })
			},
			"revert":       func() error { return s.Revert(context.Background(), cp) },
			"finalize":     func() error { return s.Finalize(context.Background(), cp) },
			"atomic-apply": func() error { return s.Apply(context.Background(), Event{}, nil) },
			"legacy-sync":  func() error { return s.SaveBlock(context.Background(), SyncBlock{}) },
			"legacy-cache": func() error { _, e := s.EnableProtocolCache(context.Background()); return e },
		} {
			if err := call(); !errors.Is(err, want) {
				t.Fatalf("%s closed=%v: got %v, want %v", name, closed, err, want)
			}
		}
	}
}
