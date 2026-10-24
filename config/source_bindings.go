package config

import (
	"encoding/json"
	"fmt"
	"go/format"
	"go/token"
	"strconv"
	"strings"
	"unicode"

	"ponder.local/ponder-go/abi"
)

// GenerateSourceContractBindings derives closed chain/event references from
// actual app definitions and ABI. The generated constructor lowers into the
// production typed source builder; it is not a compile-fixture-only schema.
func GenerateSourceContractBindings(packageName string, chains []string, rawABI json.RawMessage) ([]byte, error) {
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return nil, fmt.Errorf("invalid generated source package %q", packageName)
	}
	parsed, err := abi.Parse(string(rawABI))
	if err != nil {
		return nil, err
	}
	if len(chains) == 0 {
		return nil, fmt.Errorf("source bindings require at least one chain")
	}
	field := func(name string) string {
		var out strings.Builder
		upper := true
		for _, r := range name {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				if out.Len() == 0 && unicode.IsDigit(r) {
					out.WriteByte('X')
				}
				if upper {
					r = unicode.ToUpper(r)
				}
				out.WriteRune(r)
				upper = false
			} else {
				upper = true
			}
		}
		if out.Len() == 0 {
			return "Empty"
		}
		return out.String()
	}
	names := func(values []string) ([]string, error) {
		seen := map[string]bool{}
		out := []string{}
		for _, value := range values {
			name := field(value)
			if seen[name] {
				return nil, fmt.Errorf("source binding names collide at %q", name)
			}
			seen[name] = true
			out = append(out, name)
		}
		return out, nil
	}
	chainFields, err := names(chains)
	if err != nil {
		return nil, err
	}
	eventNames := []string{}
	seenEvents := map[string]bool{}
	for _, event := range parsed.EventEntries() {
		name := parsed.EventSafeName(event)
		if !seenEvents[name] {
			seenEvents[name] = true
			eventNames = append(eventNames, name)
		}
	}
	eventFields, err := names(eventNames)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated from actual app chains/ABI; DO NOT EDIT.\npackage %s\nimport(\"encoding/json\";\"fmt\";\"ponder.local/ponder-go/config\")\n", packageName)
	out.WriteString("type Chain struct{index int}\ntype Event struct{index int}\nvar Chains=struct{")
	for _, name := range chainFields {
		fmt.Fprintf(&out, "%s Chain;", name)
	}
	out.WriteString("}{")
	for i, name := range chainFields {
		fmt.Fprintf(&out, "%s:Chain{index:%d},", name, i+1)
	}
	out.WriteString("}\nvar Events=struct{")
	for _, name := range eventFields {
		fmt.Fprintf(&out, "%s Event;", name)
	}
	out.WriteString("}{")
	for i, name := range eventFields {
		fmt.Fprintf(&out, "%s:Event{index:%d},", name, i+1)
	}
	out.WriteString("}\n")
	out.WriteString("func(value Chain)selection()(config.SourceChainSelection,error){switch value.index{")
	for i, name := range chains {
		fmt.Fprintf(&out, "case %d:return config.SourceOnChain(%s),nil;", i+1, strconv.Quote(name))
	}
	out.WriteString("};return nil,fmt.Errorf(\"undefined generated chain reference\")}\nfunc(value Event)name()(string,error){switch value.index{")
	for i, name := range eventNames {
		fmt.Fprintf(&out, "case %d:return %s,nil;", i+1, strconv.Quote(name))
	}
	out.WriteString("};return \"\",fmt.Errorf(\"undefined generated ABI event reference\")}\n")
	out.WriteString("type Filter struct{Event Event;Args any}\ntype Contract[S,E any]struct{Chain Chain;Address config.SourceOption[any];StartBlock S;EndBlock E;Filter *Filter;IncludeTransactionReceipts config.SourceOption[bool];IncludeCallTraces config.SourceOption[bool]}\n")
	out.WriteString("func Create[S,E config.SourceBlockBound](base *config.SourceConfiguration,name string,value Contract[S,E])(config.SourceTypedConfiguration[config.SourceTypedContract[S,E]],error){\n")
	out.WriteString("var zero config.SourceTypedConfiguration[config.SourceTypedContract[S,E]];chain,err:=value.Chain.selection();if err!=nil{return zero,err}\n")
	fmt.Fprintf(&out, "contract:=config.SourceTypedContract[S,E]{Chain:chain,ABI:json.RawMessage(%s),Address:value.Address,StartBlock:value.StartBlock,EndBlock:value.EndBlock,IncludeTransactionReceipts:value.IncludeTransactionReceipts,IncludeCallTraces:value.IncludeCallTraces}\n", strconv.Quote(string(rawABI)))
	out.WriteString("if value.Filter!=nil{event,err:=value.Filter.Event.name();if err!=nil{return zero,err};contract.Filter=config.SourceSome[any](map[string]any{\"event\":event,\"args\":value.Filter.Args})}\nreturn config.CreateTypedSourceConfiguration(base,name,contract),nil}\n")
	return format.Source([]byte(out.String()))
}
