package interval

import (
	"reflect"
	"slices"
	"testing"
)

func equal(t *testing.T, got, want []Interval) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Every subtest retains the corresponding original name and every original assertion.
func TestUpstreamInterval(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []Interval
		want int64
	}{
		{"intervalSum handles empty input", []Interval{}, 0},
		{"intervalSum calculates the sum of intervals", []Interval{{1, 3}, {5, 7}, {10, 12}}, 9},
		{"intervalSum calculates the sum of single-point intervals", []Interval{{1, 1}, {3, 3}, {5, 5}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sum(tc.in); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		in, want []Interval
	}{
		{"intervalUnion handles empty input", []Interval{}, []Interval{}},
		{"intervalUnion merges overlapping intervals", []Interval{{1, 3}, {2, 4}, {6, 7}}, []Interval{{1, 4}, {6, 7}}},
		{"intervalUnion merges adjacent intervals", []Interval{{1, 3}, {4, 5}, {6, 6}}, []Interval{{1, 6}}},
		{"intervalUnion handles non-overlapping intervals", []Interval{{1, 3}, {5, 7}, {9, 12}}, []Interval{{1, 3}, {5, 7}, {9, 12}}},
		{"intervalUnion removes duplicate intervals", []Interval{{1, 3}, {1, 3}, {2, 6}, {1, 3}}, []Interval{{1, 6}}},
	} {
		t.Run(tc.name, func(t *testing.T) { equal(t, Union(tc.in), tc.want) })
	}
	t.Run("intervalUnion does not mutate inputs", func(t *testing.T) {
		in := []Interval{{3, 5}, {1, 2}, {4, 6}}
		original := slices.Clone(in)
		Union(in)
		equal(t, in, original)
	})
	for _, tc := range []struct {
		name       string
		a, b, want []Interval
	}{
		{"intervalIntersection handles empty input", []Interval{}, []Interval{}, []Interval{}},
		{"intervalIntersection correctly finds intersections", []Interval{{1, 5}, {3, 7}, {9, 12}}, []Interval{{2, 4}, {4, 6}, {10, 11}}, []Interval{{2, 6}, {10, 11}}},
		{"intervalIntersection handles no intersections", []Interval{{1, 3}, {5, 7}, {9, 12}}, []Interval{{4, 4}, {8, 8}}, []Interval{}},
	} {
		t.Run(tc.name, func(t *testing.T) { equal(t, Intersection(tc.a, tc.b), tc.want) })
	}
	for _, tc := range []struct {
		name       string
		a, b, want []Interval
	}{
		{"intervalDifference handles empty inputs", []Interval{}, []Interval{}, []Interval{}},
		{"intervalDifference correctly finds differences", []Interval{{1, 5}, {7, 10}}, []Interval{{2, 4}, {8, 10}}, []Interval{{1, 1}, {5, 5}, {7, 7}}},
		{"intervalDifference handles no difference", []Interval{{1, 3}, {5, 7}, {9, 12}}, []Interval{{0, 0}, {4, 4}}, []Interval{{1, 3}, {5, 7}, {9, 12}}},
		{"intervalDifference handles full difference", []Interval{{1, 5}, {4, 7}}, []Interval{{0, 8}}, []Interval{}},
	} {
		t.Run(tc.name, func(t *testing.T) { equal(t, Difference(tc.a, tc.b), tc.want) })
	}
	t.Run("intervalDifference does not mutate inputs", func(t *testing.T) {
		initial := []Interval{{6, 17}}
		Difference(initial, []Interval{{0, 10}, {12, 15}})
		equal(t, initial, []Interval{{6, 17}})
	})
	t.Run("sortIntervals", func(t *testing.T) {
		equal(t, Sort([]Interval{{1, 5}, {4, 7}}), []Interval{{1, 5}, {4, 7}})
		equal(t, Sort([]Interval{{4, 7}, {1, 5}}), []Interval{{1, 5}, {4, 7}})
	})
	t.Run("getChunks", func(t *testing.T) {
		got, e := Chunks(Interval{1, 9}, 2)
		if e != nil {
			t.Fatal(e)
		}
		equal(t, got, []Interval{{1, 2}, {3, 4}, {5, 6}, {7, 8}, {9, 9}})
	})
}
