package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ysh0566/ponder-go/abi"
)

func writeContractABI(out *strings.Builder, field string, source *sourceObject) (string, error) {
	items, ok := source.get("abi").([]any)
	if !ok {
		return "", fmt.Errorf("contract ABI must be an array")
	}
	raw, err := json.Marshal(sourcePlain(items))
	if err != nil {
		return "", err
	}
	typ := "ContractABI" + field
	fmt.Fprintf(out, "type %s struct{\n", typ)
	for i := range items {
		fmt.Fprintf(out, "Item%d ContractItem%d_%s\n", i, i, field)
	}
	fmt.Fprintf(out, "}\nfunc(%s)MarshalJSON()([]byte,error){return []byte(%q),nil}\n", typ, raw)
	for i, value := range items {
		raw, err := json.Marshal(sourcePlain(value))
		if err != nil {
			return "", err
		}
		item := fmt.Sprintf("ContractItem%d_%s", i, field)
		fmt.Fprintf(out, "type %s struct{}\nfunc(%s) MarshalJSON()([]byte,error){return []byte(%q),nil}\n", item, item, raw)
		var event abi.Event
		if err = json.Unmarshal(raw, &event); err != nil {
			return "", err
		}
		switch event.Type {
		case "event":
			fmt.Fprintf(out, "func(%s) Definition()abi.Event{return %#v}\n", item, event)
		case "function":
			var function abi.Function
			if err = json.Unmarshal(raw, &function); err != nil {
				return "", err
			}
			fmt.Fprintf(out, "func(%s) Definition()abi.Function{return %#v}\n", item, function)
		}
	}
	return string(raw), nil
}

type contractProperty struct {
	key, field, typ, base string
	optional              bool
	values                []string
}

func callbackProperty(out *strings.Builder, source *sourceObject, contract, key string) contractProperty {
	p := contractProperty{key: key, field: abi.GoFieldName(key)}
	values := []any{source.get(key)}
	if overrides, ok := sourceAsObject(source.get("chain")); ok {
		for _, chain := range overrides.keys {
			if obj, ok := sourceAsObject(overrides.get(chain)); ok && obj.has(key) {
				values = append(values, obj.get(key))
			}
		}
	}
	seen := map[string]bool{}
	for _, value := range values {
		if sourceNullish(value) {
			p.optional = true
			continue
		}
		literal := sourceString(value)
		if key == "address" {
			if sourceType(value) != "string" {
				p.typ = "any"
				return p
			}
			literal = strconv.Quote(strings.ToLower(literal))
			p.base = "string"
		} else {
			n, err := strconv.ParseUint(literal, 10, 64)
			if err != nil || sourceType(value) != "number" {
				p.typ = "any"
				return p
			}
			literal = strconv.FormatUint(n, 10)
			p.base = "uint64"
		}
		if !seen[literal] {
			p.values = append(p.values, literal)
			seen[literal] = true
		}
	}
	if len(p.values) == 0 {
		p.typ = "config.SourceUndefinedBound"
		return p
	}
	typ := p.field + "Of" + contract
	if len(p.values) == 1 {
		fmt.Fprintf(out, "type %s struct{}\nfunc(%s)Value()%s{return %s}\n", typ, typ, p.base, p.values[0])
	} else {
		fmt.Fprintf(out, "type %s struct{value %s}\nfunc(v %s)Value()%s{return v.value}\n", typ, p.base, typ, p.base)
	}
	p.typ = typ
	if p.optional {
		p.typ = "*" + typ
	}
	return p
}

func (p contractProperty) writeRead(out *strings.Builder, record string) {
	fmt.Fprintf(out, "{value,ok:=indexing.ContractProperty(raw,%q);", p.key)
	switch p.typ {
	case "any":
		fmt.Fprintf(out, "if ok{result.%s=value}", p.field)
	case "config.SourceUndefinedBound":
		fmt.Fprintf(out, "if ok{return %s{},fmt.Errorf(%q,value)}", record, "unexpected "+p.key+" %v")
	default:
		out.WriteString("if ok{")
		if p.base == "string" {
			fmt.Fprintf(out, "v,ok:=value.(string);if !ok{return %s{},fmt.Errorf(%q,value)};", record, "invalid "+p.key+" %v")
		} else {
			fmt.Fprintf(out, "v,err:=indexing.ContractBlockNumber(value);if err!=nil{return %s{},err};", record)
		}
		inner := strings.TrimPrefix(p.typ, "*") + "{}"
		if len(p.values) > 1 {
			inner = strings.TrimPrefix(p.typ, "*") + "{value:v}"
		}
		if p.optional {
			inner = "&" + inner
		}
		fmt.Fprintf(out, "switch v {case %s:result.%s=%s; default:return %s{},fmt.Errorf(%q,value)}", strings.Join(p.values, ","), p.field, inner, record, "unexpected "+p.key+" %v")
		out.WriteString("}")
		if !p.optional {
			fmt.Fprintf(out, "else{return %s{},fmt.Errorf(%q)}", record, "missing "+p.key)
		}
	}
	out.WriteString("}\n")
}

func writeContextContracts(cfg *SourceConfiguration, dynamicABIs []string) (string, error) {
	contracts, err := bindingGroup(cfg.value, "contracts")
	if err != nil {
		return "", err
	}
	dynamic := map[string]bool{}
	for _, name := range dynamicABIs {
		dynamic[name] = true
	}
	fields := abi.GoFieldNames(contracts.keys)
	var out strings.Builder
	out.WriteString("type CallbackContracts struct{ctx *indexing.Context}\n")
	out.WriteString("type CallbackContext[C,D any] struct{Chain C; DB D; Client *indexing.Client; Contracts CallbackContracts}\n")
	for _, name := range contracts.keys {
		source, _ := sourceAsObject(contracts.get(name))
		field := fields[name]
		record := "ContractRecord" + field
		raw := ""
		abiType := "indexing.ContractABI"
		if !dynamic[name] {
			raw, err = writeContractABI(&out, field, source)
			if err != nil {
				return "", err
			}
			abiType = "ContractABI" + field
		}
		var props []contractProperty
		for _, key := range []string{"address", "startBlock", "endBlock"} {
			props = append(props, callbackProperty(&out, source, field, key))
		}
		fmt.Fprintf(&out, "type %s struct {ABI %s\n", record, abiType)
		for _, p := range props {
			fmt.Fprintf(&out, "%s %s\n", p.field, p.typ)
		}
		fmt.Fprintf(&out, "}\nfunc(c CallbackContracts)%s()(%s,error){var result %s;raw,err:=c.ctx.ContractRecord(%q);if err!=nil{return result,err};\n", field, record, record, name)
		dst := "_"
		if dynamic[name] {
			dst = "result.ABI"
		}
		fmt.Fprintf(&out, "%s,err=indexing.ContractABIValue(raw,%q);if err!=nil{return result,err}\n", dst, raw)
		for _, p := range props {
			p.writeRead(&out, record)
		}
		out.WriteString("return result,nil}\n")
	}
	return out.String(), nil
}

// writeContextCallback pairs the registry event with a source chain and caller's schema.
func writeContextCallback(out *strings.Builder, field string, e bindingEvent, chain string) {
	ctx := "CallbackContext[" + chain + ",D]"
	fmt.Fprintf(out, "func On%s[D any](bind func(*indexing.Database)D,", field)
	if e.kind == "setup" {
		fmt.Fprintf(out, "handler func(context.Context,%s)error)config.SourceIndexingFunction{n:=Name%s{};if bind==nil||handler==nil{return n.IndexingFunction(nil)}; return n.IndexingFunction(func(ctx context.Context,args *indexing.HandlerArgs)error{if args==nil{return fmt.Errorf(\"missing setup arguments\")};c:=args.Context;", ctx, field)
	} else {
		fmt.Fprintf(out, "handler func(context.Context,Event%s,%s)error)config.SourceIndexingFunction{n:=Name%s{};if bind==nil||handler==nil{return n.IndexingFunction(nil)}; return n.On(func(ctx context.Context,e Event%s,c *indexing.Context)error{", field, ctx, field, field)
	}
	out.WriteString("chain,err:=n.Chain(c);if err!=nil{return err};return handler(ctx,")
	if e.kind != "setup" {
		out.WriteString("e,")
	}
	fmt.Fprintf(out, "%s{Chain:chain,DB:bind(c.DB),Client:c.Client,Contracts:CallbackContracts{ctx:c}})})}\n", ctx)
}
