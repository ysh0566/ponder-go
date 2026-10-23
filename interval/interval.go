// Package interval implements inclusive block intervals, matching Ponder's sync cache.
package interval

import (
	"fmt"
	"slices"
)

type Interval [2]int64

func Sum(xs []Interval) int64 {
	var n int64
	for _, x := range xs {
		n += x[1] - x[0] + 1
	}
	return n
}
func Union(xs []Interval) []Interval {
	out := []Interval{}
	if len(xs) == 0 {
		return out
	}
	xs = slices.Clone(xs)
	Sort(xs)
	current := xs[0]
	for _, next := range xs[1:] {
		if current[1] >= next[0]-1 {
			current[1] = max(current[1], next[1])
		} else {
			out = append(out, current)
			current = next
		}
	}
	return append(out, current)
}
func Intersection(a, b []Interval) []Interval {
	out := []Interval{}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		s, e := max(a[i][0], b[j][0]), min(a[i][1], b[j][1])
		if s <= e {
			out = append(out, Interval{s, e})
		}
		if a[i][1] < b[j][1] {
			i++
		} else {
			j++
		}
	}
	return Union(out)
}
func IntersectionMany(lists ...[]Interval) []Interval {
	if len(lists) == 0 {
		return []Interval{}
	}
	if len(lists) == 1 {
		return lists[0]
	}
	r := lists[0]
	for _, xs := range lists[1:] {
		r = Intersection(r, xs)
	}
	return Union(r)
}
func Difference(initial, remove []Interval) []Interval {
	a := slices.Clone(initial)
	out := []Interval{}
	i, j := 0, 0
	for i < len(a) && j < len(remove) {
		x, y := &a[i], remove[j]
		if x[1] < y[0] {
			out = append(out, *x)
			i++
		} else if y[1] < x[0] {
			j++
		} else {
			if x[0] < y[0] {
				out = append(out, Interval{x[0], y[0] - 1})
			}
			if x[1] > y[1] {
				x[0] = y[1] + 1
				j++
			} else {
				i++
			}
		}
	}
	return append(out, a[i:]...)
}
func Bounds(xs []Interval) (Interval, bool) {
	if len(xs) == 0 {
		return Interval{}, false
	}
	r := xs[0]
	for _, x := range xs[1:] {
		r[0] = min(r[0], x[0])
		r[1] = max(r[1], x[1])
	}
	return r, true
}
func Sort(xs []Interval) []Interval {
	slices.SortStableFunc(xs, func(a, b Interval) int {
		if a[0] < b[0] {
			return -1
		}
		if a[0] > b[0] {
			return 1
		}
		return 0
	})
	return xs
}
func Chunks(x Interval, size int64) ([]Interval, error) {
	if size <= 0 {
		return nil, fmt.Errorf("chunk size must be positive")
	}
	out := []Interval{}
	for start := x[0]; start <= x[1]; {
		end := min(start+size-1, x[1])
		out = append(out, Interval{start, end})
		if end == x[1] {
			break
		}
		start = end + 1
	}
	return out, nil
}
