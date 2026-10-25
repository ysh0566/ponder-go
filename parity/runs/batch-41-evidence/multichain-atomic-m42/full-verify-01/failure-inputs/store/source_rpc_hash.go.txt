package store

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"ponder.local/ponder-go/filter"
)

// SourceRPCRequestHash is the pinned SyncStore key, separate from the native
// protocol cache's chain/scope/provenance SHA256 key. Raw source values retain
// undefined and invalid typed request shapes at this runtime boundary.
func SourceRPCRequestHash(request any) (string, error) {
	encoded, err := sourceOrderedJSON(request)
	if err == nil && encoded == nil {
		return "", fmt.Errorf("source serialized request is undefined")
	}
	if err != nil {
		return "", err
	}
	// Default Unicode lowercasing includes expansion and contextual sigma.
	text := cases.Lower(language.Und).String(string(encoded))
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:]), nil
}
func sourceJSONString(value string) []byte {
	var b bytes.Buffer
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.Bytes()
}
func sourceNumber(n float64) string {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return "null"
	}
	if n == 0 {
		return "0"
	}
	a := math.Abs(n)
	format := byte('f')
	if a < 1e-6 || a >= 1e21 {
		format = 'e'
	}
	text := strconv.FormatFloat(n, format, -1, 64)
	if i := strings.IndexByte(text, 'e'); i >= 0 {
		exponent, _ := strconv.Atoi(text[i+1:])
		sign := ""
		if exponent >= 0 {
			sign = "+"
		}
		text = text[:i+1] + sign + strconv.Itoa(exponent)
	}
	return text
}
func sourceIndex(s string) (uint64, bool) {
	n, e := strconv.ParseUint(s, 10, 32)
	return n, e == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == s
}
func sourceKeyLess(a, b string) bool {
	ai, ax := sourceIndex(a)
	bi, bx := sourceIndex(b)
	if ax || bx {
		if ax && bx {
			return ai < bi
		}
		return ax
	}
	au, bu := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] != bu[i] {
			return au[i] < bu[i]
		}
	}
	return len(au) < len(bu)
}
func sourceOrderedJSON(value any) ([]byte, error) {
	if filter.IsUndefined(value) {
		return nil, nil
	}
	switch v := value.(type) {
	case nil:
		return nil, fmt.Errorf("Cannot convert undefined or null to object")
	case map[string]any:
		if v == nil {
			return nil, fmt.Errorf("Cannot convert undefined or null to object")
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return sourceKeyLess(keys[i], keys[j]) })
		var b bytes.Buffer
		b.WriteByte('{')
		written := false
		for _, k := range keys {
			encoded, e := sourceOrderedJSON(v[k])
			if e != nil {
				return nil, e
			}
			if encoded == nil || k == "__proto__" {
				continue
			}
			if written {
				b.WriteByte(',')
			}
			written = true
			b.Write(sourceJSONString(k))
			b.WriteByte(':')
			b.Write(encoded)
		}
		b.WriteByte('}')
		return b.Bytes(), nil
	case []any:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			raw, e := sourceOrderedJSON(item)
			if e != nil {
				return nil, e
			}
			if raw == nil {
				raw = []byte("null")
			}
			b.Write(raw)
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	case string:
		return sourceJSONString(v), nil
	case bool:
		if v {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case float64:
		return []byte(sourceNumber(v)), nil
	case float32:
		return []byte(sourceNumber(float64(v))), nil
	case json.Number:
		n, e := strconv.ParseFloat(string(v), 64)
		if e != nil {
			return nil, e
		}
		return []byte(sourceNumber(n)), nil
	case int:
		return []byte(sourceNumber(float64(v))), nil
	case int64:
		return []byte(sourceNumber(float64(v))), nil
	case uint64:
		return []byte(sourceNumber(float64(v))), nil
	case *big.Int:
		return nil, fmt.Errorf("Do not know how to serialize a BigInt")
	default:
		// Decode normal typed slices/structs through their JSON representation.
		// Undefined must be passed as map[string]any/[]any so it stays explicit.
		raw, e := json.Marshal(value)
		if e != nil {
			return nil, e
		}
		var out any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if e = decoder.Decode(&out); e != nil {
			return nil, e
		}
		return sourceOrderedJSON(out)
	}
}
