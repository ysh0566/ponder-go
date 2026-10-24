package checkpoint

import (
	"strings"
	"testing"
)

// One subtest per original packages/core/src/utils/checkpoint.test.ts test.
func TestUpstreamCheckpoint(t *testing.T) {
	one := Checkpoint{1, 1, 1, 1, 1, 1}
	encoding := "0000000001" + strings.Repeat("0", 15) + "1" + strings.Repeat("0", 15) + "1" + strings.Repeat("0", 15) + "1" + "1" + strings.Repeat("0", 15) + "1"
	t.Run("encodeCheckpoint produces expected encoding", func(t *testing.T) {
		got, e := Encode(one)
		if e != nil || got != encoding {
			t.Fatalf("%s %v", got, e)
		}
	})
	t.Run("decodeCheckpoint produces expected object", func(t *testing.T) {
		got, e := Decode(encoding)
		if e != nil || got != one {
			t.Fatalf("%+v %v", got, e)
		}
	})
	t.Run("decodeCheckpoint decodes an encoded maxCheckpoint", func(t *testing.T) {
		got, e := Decode(MaxString)
		if e != nil || got != Max {
			t.Fatalf("%+v %v", got, e)
		}
	})
	t.Run("isCheckpointEqual returns true if checkpoints are the same", func(t *testing.T) {
		if !Equal(one, one) {
			t.Fatal("expected true")
		}
	})
	t.Run("isCheckpointEqual returns false if checkpoints are different", func(t *testing.T) {
		two := one
		two.ChainID = 2
		if Equal(one, two) {
			t.Fatal("expected false")
		}
	})
	comparisons := []struct {
		name string
		a, b Checkpoint
		want bool
	}{
		{"isCheckpointGreaterThan compares correctly on blockTimestamp", one, Checkpoint{2, 2, 1, 1, 1, 1}, false},
		{"isCheckpointGreaterThan compares correctly on chainId", one, Checkpoint{1, 2, 1, 1, 1, 1}, false},
		{"isCheckpointGreaterThan compares correctly on transactionIndex", Checkpoint{1, 1, 1, 5, 1, 1}, Checkpoint{1, 1, 1, 4, 1, 1}, true},
		{"isCheckpointGreaterThan compares correctly on eventType", Checkpoint{1, 1, 1, 1, 5, 1}, Checkpoint{1, 1, 1, 1, 4, 1}, true},
		{"isCheckpointGreaterThan compares correctly on eventIndex", Checkpoint{1, 1, 1, 1, 1, 5}, Checkpoint{1, 1, 1, 1, 1, 4}, true},
		{"isCheckpointGreaterThan compares correctly with multiple values", Checkpoint{6, 5, 9, 1, 1, 12}, Checkpoint{6, 5, 10, 1, 1, 4}, false},
		{"isCheckpointGreaterThan returns false for equal checkpoints", Checkpoint{6, 5, 9, 1, 1, 12}, Checkpoint{6, 5, 9, 1, 1, 12}, false},
	}
	for _, tc := range comparisons {
		t.Run(tc.name, func(t *testing.T) {
			if got := Greater(tc.a, tc.b); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	t.Run("checkpointMax returns correct value if only one checkpoint", func(t *testing.T) {
		got, e := Maximum(one)
		if e != nil || got != one {
			t.Fatalf("%+v %v", got, e)
		}
	})
	t.Run("checkpointMax compares properly on timestamp", func(t *testing.T) {
		two := Checkpoint{2, 1, 1, 1, 1, 1}
		got, e := Maximum(one, two)
		if e != nil || got != two {
			t.Fatalf("%+v %v", got, e)
		}
	})
	t.Run("checkpointMin compares properly on blockNumber", func(t *testing.T) {
		two := Checkpoint{2, 1, 3, 1, 1, 1}
		three := Checkpoint{1, 1, 1, 1, 1, 99}
		got, e := Minimum(one, two, three)
		if e != nil || got != one {
			t.Fatalf("%+v %v", got, e)
		}
	})
}

func TestEncodingBounds(t *testing.T) {
	for _, c := range []Checkpoint{{EventType: -1}, {EventType: 10}, {BlockTimestamp: 10_000_000_000}, {ChainID: 10_000_000_000_000_000}} {
		if _, e := Encode(c); e == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	for _, s := range []string{"", strings.Repeat("x", 75), ZeroString + "0"} {
		if _, e := Decode(s); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
