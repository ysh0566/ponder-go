package abi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MergeABIs combines contract declarations in input order. Matching follows
// viem's formatted name and input types, including tuple and array dimensions.
// The first item supplies all remaining metadata.
func MergeABIs(inputs ...json.RawMessage) ([]json.RawMessage, error) {
	merged := []json.RawMessage{}
	seen := map[string]bool{}
	for i, input := range inputs {
		var items []json.RawMessage
		if err := json.Unmarshal(input, &items); err != nil || items == nil {
			return nil, fmt.Errorf("ABI %d must be a JSON array", i)
		}
		for j, raw := range items {
			var item Event
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, fmt.Errorf("ABI %d item %d: %w", i, j, err)
			}
			switch item.Type {
			case "constructor", "receive", "fallback":
				continue
			case "function", "event", "error":
			default:
				return nil, fmt.Errorf("ABI %d item %d has unsupported type %q", i, j, item.Type)
			}
			if item.Name == "" {
				return nil, fmt.Errorf("ABI %d item %d has no name", i, j)
			}
			key := item.Name + "(" + mergeParameters(item.Inputs) + ")"
			if !seen[key] {
				merged = append(merged, raw)
				seen[key] = true
			}
		}
	}
	return merged, nil
}

func mergeParameters(inputs []Parameter) string {
	types := make([]string, len(inputs))
	for i, p := range inputs {
		types[i] = p.Type
		if suffix, ok := strings.CutPrefix(p.Type, "tuple"); ok {
			types[i] = "(" + mergeParameters(p.Components) + ")" + suffix
		}
	}
	return strings.Join(types, ",")
}

// GenerateMergedCatalog creates a typed view of the ordered result of MergeABIs.
// Each position has the concrete declaration type selected from the source ABIs.
func GenerateMergedCatalog(packageName string, inputs ...json.RawMessage) ([]byte, error) {
	items, err := MergeABIs(inputs...)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	parsed, err := Parse(string(data))
	if err != nil {
		return nil, err
	}
	return generateCatalog(packageName, data, parsed.eventEntries, parsed.functionEntries, items)
}

func writeMergedItems(out *strings.Builder, items []json.RawMessage, types map[string]string) error {
	var fields bytes.Buffer
	for i, raw := range items {
		var item Event
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		var typ string
		switch item.Type {
		case "event":
			typ = types[fmt.Sprintf("%#v", item)]
		case "function":
			var function Function
			if err := json.Unmarshal(raw, &function); err != nil {
				return err
			}
			typ = types[fmt.Sprintf("%#v", function)]
		case "error":
			typ = fmt.Sprintf("ErrorItem%d", i)
			fmt.Fprintf(out, "type %s struct{}\nfunc(%s)isABIDeclaration(){}\nfunc(%s)MarshalJSON()([]byte,error){return []byte(%q),nil}\n", typ, typ, typ, raw)
		}
		if typ == "" {
			return fmt.Errorf("missing generated declaration for ABI item %d", i)
		}
		fmt.Fprintf(&fields, "Item%d %s\n", i, typ)
	}
	fmt.Fprintf(out, "type ContractItems struct{\n%s}\nfunc(ContractABI)Items() ContractItems{return ContractItems{}}\n", fields.String())
	return nil
}
