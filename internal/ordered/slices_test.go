package ordered

import (
	"cmp"
	"errors"
	"reflect"
	"testing"
)

func TestSourceOrderedSlices(t *testing.T) {
	t.Run("zipper", func(t *testing.T) {
		got := Merge([]int{1, 3, 5}, []int{2, 4, 6}, cmp.Compare[int])
		if !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5, 6}) {
			t.Fatal(got)
		}
	})
	t.Run("zipperMany", func(t *testing.T) {
		got := MergeMany([][]int{{1, 3, 5}, {2, 4, 6}, {7, 8, 9}}, cmp.Compare[int])
		if !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}) {
			t.Fatal(got)
		}
	})
	t.Run("chunk", func(t *testing.T) {
		result := Chunks([]int{1, 2, 3, 4, 5, 6, 7, 8, 9}, 3)
		if !reflect.DeepEqual(result, [][]int{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}) {
			t.Fatal(result)
		}
		if got := Chunks([]int{}, 3); !reflect.DeepEqual(got, [][]int{}) {
			t.Fatal(got)
		}
	})
	t.Run("partition", func(t *testing.T) {
		all := []int{1, 2, 3, 4, 5}
		for _, tc := range []struct {
			input       []int
			pred        func(int) bool
			left, right []int
		}{
			{all, func(n int) bool { return n <= 2 }, []int{1, 2}, []int{3, 4, 5}},
			{all, func(n int) bool { return n < 6 }, all, []int{}},
			{[]int{2, 5}, func(n int) bool { return n <= 5 }, []int{2, 5}, []int{}},
			{[]int{1}, func(n int) bool { return n > 1 }, []int{}, []int{1}},
			{[]int{1, 2, 3}, func(n int) bool { return n > 0 }, []int{1, 2, 3}, []int{}},
			{[]int{1, 2, 3}, func(n int) bool { return n > 5 }, []int{}, []int{1, 2, 3}},
		} {
			l, r, err := Partition(tc.input, func(n int) (bool, error) { return tc.pred(n), nil })
			if err != nil || !reflect.DeepEqual(l, tc.left) || !reflect.DeepEqual(r, tc.right) {
				t.Fatalf("%v -> %v, %v, %v", tc.input, l, r, err)
			}
		}
	})
}

func TestOrderedBoundaries(t *testing.T) {
	boom := errors.New("malformed interior checkpoint")
	if _, _, err := Partition([]int{1, 2, 9}, func(n int) (bool, error) {
		if n == 2 {
			return false, boom
		}
		return n < 5, nil
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	type event struct {
		key   int
		chain string
	}
	items := MergeMany([][]event{{{1, "a"}}, {{1, "b"}}, {{1, "c"}}}, func(a, b event) int { return cmp.Compare(a.key, b.key) })
	if !reflect.DeepEqual(items, []event{{1, "c"}, {1, "b"}, {1, "a"}}) {
		t.Fatal(items)
	}
	input := []int{1, 2, 3}
	group := Chunks(input, 2)
	group[0][0] = 9
	if input[0] != 1 || !reflect.DeepEqual(group[1], []int{3}) {
		t.Fatal(input, group)
	}
}
