package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// MarshalJSON retains SQL column order. The original client uses Object.values
// on every row, so Go's alphabetically sorted map encoding is incompatible.
func (r Result) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(`{"rows":[`)
	for i, row := range r.Rows {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		keys := []string{}
		seen := map[string]bool{}
		for _, field := range r.Fields {
			name := fmt.Sprint(field["name"])
			if _, ok := row[name]; ok && !seen[name] {
				keys = append(keys, name)
				seen[name] = true
			}
		}
		rest := []string{}
		for key := range row {
			if !seen[key] {
				rest = append(rest, key)
			}
		}
		sort.Strings(rest)
		keys = append(keys, rest...)
		for j, key := range keys {
			if j > 0 {
				out.WriteByte(',')
			}
			name, _ := json.Marshal(key)
			value, e := json.Marshal(row[key])
			if e != nil {
				return nil, e
			}
			out.Write(name)
			out.WriteByte(':')
			out.Write(value)
		}
		out.WriteByte('}')
	}
	out.WriteByte(']')
	if len(r.Fields) > 0 {
		fields, e := json.Marshal(r.Fields)
		if e != nil {
			return nil, e
		}
		out.WriteString(`,"fields":`)
		out.Write(fields)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
