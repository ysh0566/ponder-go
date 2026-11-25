// Package ordered provides the slice operations used to assemble source event batches.
package ordered

// Partition splits a sequence whose predicate switches from true to false.
// Boundary checks avoid touching interior records outside a partial batch.
func Partition[T any](values []T, predicate func(T) (bool, error)) ([]T, []T, error) {
	empty := []T{}
	if len(values) == 0 {
		return empty, empty, nil
	}
	first, err := predicate(values[0])
	if err != nil {
		return nil, nil, err
	}
	if !first {
		return empty, values, nil
	}
	last, err := predicate(values[len(values)-1])
	if err != nil {
		return nil, nil, err
	}
	if last {
		return values, empty, nil
	}
	lo, hi := 0, len(values)
	for lo < hi {
		mid := lo + (hi-lo)/2
		ok, err := predicate(values[mid])
		if err != nil {
			return nil, nil, err
		}
		if ok {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return append([]T{}, values[:lo]...), append([]T{}, values[lo:]...), nil
}

// Merge merges two sorted slices. Ties take the right-hand input first.
func Merge[T any](left, right []T, compare func(T, T) int) []T {
	out := make([]T, 0, len(left)+len(right))
	for len(left) > 0 && len(right) > 0 {
		if compare(left[0], right[0]) < 0 {
			out = append(out, left[0])
			left = left[1:]
		} else {
			out = append(out, right[0])
			right = right[1:]
		}
	}
	out = append(out, left...)
	return append(out, right...)
}

func MergeMany[T any](inputs [][]T, compare func(T, T) int) []T {
	if len(inputs) == 0 {
		return []T{}
	}
	result := inputs[0]
	for _, next := range inputs[1:] {
		result = Merge(result, next, compare)
	}
	return result
}

// Chunks copies consecutive groups of size elements.
func Chunks[T any](values []T, size int) [][]T {
	if size <= 0 {
		panic("chunk size must be positive")
	}
	out := [][]T{}
	for len(values) > 0 {
		n := min(len(values), size)
		out = append(out, append([]T{}, values[:n]...))
		values = values[n:]
	}
	return out
}
