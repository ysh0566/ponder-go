package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func multichainInitialStates() []CheckpointState {
	states := []CheckpointState{}
	for _, c := range []struct {
		id   uint64
		name string
	}{{1, "mainnet"}, {10, "optimism"}} {
		cp := checkpoint.MustEncode(checkpoint.Block(100, c.id, 1, true))
		states = append(states, CheckpointState{ChainID: c.id, ChainName: c.name, LatestCheckpoint: cp, SafeCheckpoint: cp, FinalizedCheckpoint: cp})
	}
	return states
}
func TestMultichainPrimitiveValidation(t *testing.T) {
	tests := map[string]func([]CheckpointState) []CheckpointState{
		"empty":            func(s []CheckpointState) []CheckpointState { return nil },
		"duplicate-id":     func(s []CheckpointState) []CheckpointState { s[1] = s[0]; s[1].ChainName = "other"; return s },
		"duplicate-name":   func(s []CheckpointState) []CheckpointState { s[1].ChainName = s[0].ChainName; return s },
		"wrong-checkpoint": func(s []CheckpointState) []CheckpointState { s[1].SafeCheckpoint = s[0].SafeCheckpoint; return s },
		"unequal-state":    func(s []CheckpointState) []CheckpointState { s[1].LatestCheckpoint = checkpoint.ZeroString; return s },
		"malformed":        func(s []CheckpointState) []CheckpointState { s[1].SafeCheckpoint = "bad"; return s },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			// nil backend proves validation precedes transactions and callbacks.
			a := &Actions{}
			if err := a.InitializeMultichain(context.Background(), change(multichainInitialStates()), func(*Writer, func(uint64) error) error { t.Fatal("invalid callback ran"); return nil }); err == nil {
				t.Fatal("invalid states accepted")
			}
		})
	}
}

func TestMultichainPrimitiveAtomicSetupAndGroups(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			a := sourceActions(t, dialect, sourceActionsModel(), schema.Multichain)
			states := multichainInitialStates()
			sentinel := errors.New("second-chain failure")
			err := a.InitializeMultichain(ctx, states, func(w *Writer, attribute func(uint64) error) error {
				if err := attribute(1); err != nil {
					return err
				}
				if err := w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert); err != nil {
					return err
				}
				if err := attribute(10); err != nil {
					return err
				}
				return sentinel
			})
			if !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			rows, err := a.Select(ctx, "account")
			if err != nil || len(rows) != 0 {
				t.Fatalf("setup leaked rows %v %v", rows, err)
			}
			cps, err := a.Checkpoints(ctx)
			if err != nil || len(cps) != 0 {
				t.Fatalf("setup leaked checkpoints %v %v", cps, err)
			}
			var escaped func(uint64) error
			err = a.InitializeMultichain(ctx, states, func(w *Writer, attribute func(uint64) error) error {
				escaped = attribute
				db := w.Indexing(CacheOptions{Complete: true})
				if err := attribute(1); err != nil {
					return err
				}
				if _, err := db.InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Conflict{}); err != nil {
					return err
				}
				// Same initial checkpoint must still drain/seal the buffered segment.
				if err := attribute(1); err != nil {
					return err
				}
				if _, err := db.Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": "20"}); err != nil {
					return err
				}
				if err := attribute(10); err != nil {
					return err
				}
				if _, err := db.Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": "30"}); err != nil {
					return err
				}
				if err := attribute(1); err != nil {
					return err
				}
				journal, err := w.tx.Query(ctx, "SELECT chain_id::text AS chain,checkpoint FROM "+a.s.internal("_ponder_go_journal")+" ORDER BY operation_id")
				if err != nil {
					return err
				}
				if len(journal.Rows) != 3 {
					t.Fatalf("missing sealed segments: %#v", journal.Rows)
				}
				for i, want := range []string{"1", "1", "10"} {
					if fmt.Sprint(journal.Rows[i]["chain"]) != want {
						t.Fatalf("segment %d misattributed: %#v", i, journal.Rows)
					}
				}
				if journal.Rows[0]["checkpoint"] != states[0].SafeCheckpoint || journal.Rows[2]["checkpoint"] != states[1].SafeCheckpoint {
					t.Fatal(journal.Rows)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := escaped(1); err == nil {
				t.Fatal("escaped attribution remained live")
			}
			rows, err = a.Select(ctx, "account")
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			expectSourceBalance(t, rows[0], 30)
			if err := a.InitializeMultichain(ctx, states, func(*Writer, func(uint64) error) error { t.Fatal("duplicate setup reran"); return nil }); err == nil {
				t.Fatal("duplicate setup accepted")
			}
			target := checkpoint.Block(101, 10, 2, true)
			applied, err := a.ProcessHistoricalGroup(ctx, "optimism", target, func(w *Writer) error {
				_, err := w.Indexing().Update(ctx, "account", Row{"address": sourceZeroAddress}, Row{"balance": "40"})
				return err
			})
			if err != nil || !applied {
				t.Fatal(applied, err)
			}
			applied, err = a.ProcessHistoricalGroup(ctx, "optimism", target, func(*Writer) error { t.Fatal("committed group replayed"); return nil })
			if err != nil || applied {
				t.Fatal(applied, err)
			}
			empty := checkpoint.Block(102, 1, 3, true)
			applied, err = a.ProcessHistoricalGroup(ctx, "mainnet", empty, func(*Writer) error { return nil })
			if err != nil || !applied {
				t.Fatal(applied, err)
			}
			if cp, err := a.RuntimeCheckpoint(ctx, 1); err != nil || cp != checkpoint.MustEncode(empty) {
				t.Fatal(cp, err)
			}
			if _, err = a.ProcessHistoricalGroup(ctx, "wrong", checkpoint.Block(103, 1, 4, true), func(*Writer) error { t.Fatal("wrong chain ran"); return nil }); err == nil {
				t.Fatal("wrong chain accepted")
			}
			if err = a.Revert(ctx, checkpoint.Block(100, 1, 1, true)); err == nil {
				t.Fatal("safe prefix rollback accepted")
			}
		})
	}
}

type multichainFaultBackend struct {
	Backend
	match  string
	commit string
}
type multichainFaultTx struct {
	Transaction
	owner *multichainFaultBackend
}

func (b *multichainFaultBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, err := b.Backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &multichainFaultTx{tx, b}, nil
}
func (t *multichainFaultTx) Exec(ctx context.Context, q string, args ...any) error {
	if t.owner.match != "" && strings.Contains(q, t.owner.match) {
		return errors.New("injected multichain SQL failure")
	}
	return t.Transaction.Exec(ctx, q, args...)
}
func (t *multichainFaultTx) Commit(ctx context.Context) error {
	if t.owner.commit == "before" {
		return errors.New("injected commit failure")
	}
	if err := t.Transaction.Commit(ctx); err != nil {
		return err
	}
	if t.owner.commit == "after" {
		return errors.New("injected lost commit acknowledgement")
	}
	return nil
}
func TestMultichainPrimitiveFailuresAndCommitEvidence(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, fault := range []string{"flush", "seal", "metadata", "commit-before", "commit-after"} {
				t.Run(fault, func(t *testing.T) {
					ctx := context.Background()
					a := sourceActions(t, dialect, sourceActionsModel(), schema.Multichain)
					backend := &multichainFaultBackend{Backend: a.s.Backend}
					a.s.Backend = backend
					switch fault {
					case "flush":
						backend.match = "SAVEPOINT ponder_go_flush"
					case "seal":
						backend.match = "SET checkpoint=$1,chain_id=$2"
					case "metadata":
						backend.match = "INSERT INTO " + a.s.internal("_ponder_go_finalized")
					case "commit-before":
						backend.commit = "before"
					case "commit-after":
						backend.commit = "after"
					}
					err := a.InitializeMultichain(ctx, multichainInitialStates(), func(w *Writer, attribute func(uint64) error) error {
						if err := attribute(1); err != nil {
							return err
						}
						_, err := w.Indexing().InsertOne(ctx, "account", Row{"address": sourceZeroAddress, "balance": "1"}, Conflict{})
						return err
					})
					if err == nil {
						t.Fatal("fault did not fail")
					}
					var uncertain *CommitOutcomeUnknownError
					if errors.As(err, &uncertain) != strings.HasPrefix(fault, "commit-") {
						t.Fatalf("wrong error boundary %T %v", err, err)
					}
					backend.match = ""
					backend.commit = ""
					rows, e := a.Select(ctx, "account")
					if e != nil {
						t.Fatal(e)
					}
					states, e := a.Checkpoints(ctx)
					if e != nil {
						t.Fatal(e)
					}
					if fault == "commit-after" {
						if len(rows) != 1 || len(states) != 2 {
							t.Fatal("lost ack lacks durable evidence", rows, states)
						}
					} else if len(rows) != 0 || len(states) != 0 {
						t.Fatal("failed transaction leaked", rows, states)
					}
					if fault != "commit-after" {
						if err := a.InitializeMultichain(ctx, multichainInitialStates(), func(*Writer, func(uint64) error) error { return nil }); err != nil {
							t.Fatal(err)
						}
					}
					// Historical lost acknowledgments resolve to durable cursor, so replay
					// cannot invoke the handler again even though the first call returned false.
					backend.commit = "after"
					target := checkpoint.Block(101, 1, 2, true)
					applied, e := a.ProcessHistoricalGroup(ctx, "mainnet", target, func(*Writer) error { return nil })
					if applied || !errors.As(e, &uncertain) {
						t.Fatal(applied, e)
					}
					backend.commit = ""
					if cp, e := a.RuntimeCheckpoint(ctx, 1); e != nil || cp != checkpoint.MustEncode(target) {
						t.Fatal(cp, e)
					}
					applied, e = a.ProcessHistoricalGroup(ctx, "mainnet", target, func(*Writer) error { t.Fatal("lost ack handler replayed"); return nil })
					if applied || e != nil {
						t.Fatal(applied, e)
					}
				})
			}
		})
	}
}
