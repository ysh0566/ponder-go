package metrics

import "unicode/utf16"

// Truncate shortens event names for display using the source's UTF-16 lengths.
// Go strings use the replacement rune when a boundary splits a surrogate pair.
func Truncate(s string, maxLength ...int) string {
	limit := 36
	if len(maxLength) > 0 {
		limit = maxLength[0]
	}
	units := utf16.Encode([]rune(s))
	if len(units) <= limit {
		return s
	}
	return string(utf16.Decode(TruncateUTF16(units, limit)))
}

// TruncateUTF16 retains exact JavaScript code units, including lone surrogates.
func TruncateUTF16(units []uint16, limit int) []uint16 {
	if len(units) <= limit {
		return units
	}
	prefix, suffix := limit/2-2, limit/2-2
	if limit%2 > 0 {
		suffix++
	} else if limit%2 < 0 {
		prefix--
	}
	index := func(n int) int {
		if n < 0 {
			return max(0, len(units)+n)
		}
		return min(n, len(units))
	}
	out := append([]uint16{}, units[:index(prefix)]...)
	out = append(out, '.', '.', '.')
	return append(out, units[index(-suffix):]...)
}
