package config

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"math/big"
	"strconv"
	"strings"

	"ponder.local/ponder-go/abi"
)

type bindingSource struct {
	events []bindingEvent
	chains []string
}

type bindingApplication struct {
	chainNames    []string
	ids           map[string]uint64
	sources       []bindingSource
	contextChains map[string]bool
}

func bindingGroup(root *sourceObject, key string) (*sourceObject, error) {
	if !root.has(key) {
		return sourceNewObject(), nil
	}
	group, err := sourceObjects(root.get(key))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return group, nil
}

type bindingEvent struct {
	name, kind      string
	inputs, outputs []abi.Parameter
	receipt         receiptPresence
}
type receiptPresence int

const (
	noReceipt receiptPresence = iota
	optionalReceipt
	requiredReceipt
)

// The source type includes the base value (undefined when absent) and every explicit override.
func bindingFlag(source *sourceObject, key string) (set, unset bool) {
	set = source.get(key) == true
	unset = !set
	if overrides, ok := sourceAsObject(source.get("chain")); ok {
		for _, chain := range overrides.keys {
			if override, ok := sourceAsObject(overrides.get(chain)); ok && override.has(key) {
				set = set || override.get(key) == true
				unset = unset || override.get(key) != true
			}
		}
	}
	return
}

func bindingSourceEvents(group, name string, source *sourceObject, dynamic bool) ([]bindingEvent, error) {
	if group == "blocks" {
		return []bindingEvent{{name: name + ":block", kind: "block"}}, nil
	}
	receipt := noReceipt
	if set, unset := bindingFlag(source, "includeTransactionReceipts"); set {
		receipt = requiredReceipt
		if unset {
			receipt = optionalReceipt
		}
	}
	if group == "accounts" {
		var events []bindingEvent
		for _, kind := range []string{"transaction", "transfer"} {
			presence := receipt
			if kind == "transaction" {
				presence = requiredReceipt
			}
			for _, direction := range []string{"from", "to"} {
				events = append(events, bindingEvent{name: name + ":" + kind + ":" + direction, kind: kind, receipt: presence})
			}
		}
		return events, nil
	}
	result := []bindingEvent{{name: name + ":setup", kind: "setup"}}
	if dynamic {
		return result, nil
	}
	parsed, _, err := sourceParseABI(source.get("abi"))
	if err != nil {
		return nil, err
	}
	events, functions, err := parsed.DeclarationItems()
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		result = append(result, bindingEvent{name: name + ":" + e.Name, kind: "log", inputs: e.Item.Inputs, receipt: receipt})
	}
	if traces, _ := bindingFlag(source, "includeCallTraces"); traces {
		for _, f := range functions {
			result = append(result, bindingEvent{name: name + "." + f.Name, kind: "trace", inputs: f.Item.Inputs, outputs: f.Item.Outputs, receipt: receipt})
		}
	}
	return result, nil
}

func readBindingApplication(cfg *SourceConfiguration, dynamicABIs []string) (*bindingApplication, error) {
	if cfg == nil || cfg.value == nil {
		return nil, fmt.Errorf("application configuration is required")
	}
	chains, err := bindingGroup(cfg.value, "chains")
	if err != nil {
		return nil, err
	}
	app := &bindingApplication{chainNames: chains.entries(), ids: map[string]uint64{}, contextChains: map[string]bool{}}
	for _, name := range chains.keys {
		chain, err := sourceObjects(chains.get(name))
		if err != nil || name == "" {
			return nil, fmt.Errorf("invalid chain %q", name)
		}
		id, ok := new(big.Rat).SetString(sourceString(chain.get("id")))
		if sourceType(chain.get("id")) != "number" || !ok || !id.IsInt() || !id.Num().IsUint64() {
			return nil, fmt.Errorf("invalid ID for chain %q", name)
		}
		app.ids[name] = id.Num().Uint64()
	}
	dynamic := map[string]bool{}
	for _, name := range dynamicABIs {
		dynamic[name] = true
	}
	registered := map[string]bool{}
	for _, groupName := range []string{"contracts", "accounts", "blocks"} {
		group, err := bindingGroup(cfg.value, groupName)
		if err != nil {
			return nil, err
		}
		for _, name := range group.keys {
			source, err := sourceObjects(group.get(name))
			if err != nil || name == "" {
				return nil, fmt.Errorf("invalid %s source %q", groupName, name)
			}
			if overrides, ok := sourceAsObject(source.get("chain")); ok {
				for _, chain := range overrides.keys {
					if _, err = sourceObjects(overrides.get(chain)); err != nil {
						return nil, fmt.Errorf("%s %s chain %s: %w", groupName, name, chain, err)
					}
				}
			}
			events, err := bindingSourceEvents(groupName, name, source, groupName == "contracts" && dynamic[name])
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", groupName, name, err)
			}
			if groupName == "contracts" {
				delete(dynamic, name)
			}
			for _, event := range events {
				if registered[event.name] {
					return nil, fmt.Errorf("ambiguous indexing function name %q", event.name)
				}
				registered[event.name] = true
			}
			var selected []string
			if len(chains.keys) > 0 {
				switch v := source.get("chain").(type) {
				case string:
					selected = []string{v}
				case *sourceObject:
					selected = v.entries()
				default:
					return nil, fmt.Errorf("source %q must select a chain", name)
				}
				for _, chain := range selected {
					if _, ok := app.ids[chain]; !ok {
						return nil, fmt.Errorf("source %q selects unknown chain %q", name, chain)
					}
					if groupName != "accounts" {
						app.contextChains[chain] = true
					}
				}
			}
			app.sources = append(app.sources, bindingSource{events, selected})
		}
	}
	for _, name := range dynamicABIs {
		if dynamic[name] {
			return nil, fmt.Errorf("unknown dynamic ABI contract %q", name)
		}
	}
	return app, nil
}

// GenerateApplicationBindings emits callback names and chain types for an application.
// Contracts listed in dynamicABIs have a runtime ABI with no statically known items.
func GenerateApplicationBindings(pkg string, cfg *SourceConfiguration, dynamicABIs ...string) ([]byte, error) {
	return generateApplicationBindings(pkg, cfg, false, false, dynamicABIs)
}

// GenerateApplicationEventBindings includes the typed event projection and On handler for each callback.
func GenerateApplicationEventBindings(pkg string, cfg *SourceConfiguration, dynamicABIs ...string) ([]byte, error) {
	return generateApplicationBindings(pkg, cfg, true, false, dynamicABIs)
}

// GenerateApplicationRegistry adds callbacks with a source-specific chain,
// configured contract bindings and a database bound to the caller's schema.
func GenerateApplicationRegistry(pkg string, cfg *SourceConfiguration, dynamicABIs ...string) ([]byte, error) {
	return generateApplicationBindings(pkg, cfg, true, true, dynamicABIs)
}

func generateApplicationBindings(pkg string, cfg *SourceConfiguration, eventTypes, registry bool, dynamicABIs []string) ([]byte, error) {
	if !token.IsIdentifier(pkg) || pkg == "_" {
		return nil, fmt.Errorf("invalid package name %q", pkg)
	}
	app, err := readBindingApplication(cfg, dynamicABIs)
	if err != nil {
		return nil, err
	}
	var out strings.Builder

	var names []string
	for _, source := range app.sources {
		for _, e := range source.events {
			names = append(names, e.name)
		}
	}
	fields := abi.GoFieldNames(names)
	var payloads strings.Builder
	if eventTypes {
		for _, source := range app.sources {
			for _, e := range source.events {
				if e.kind == "setup" {
					continue
				}
				if err := writeBindingEvent(&payloads, fields[e.name], e); err != nil {
					return nil, fmt.Errorf("%s: %w", e.name, err)
				}
			}
		}
	}
	if registry {
		contracts, err := writeContextContracts(cfg, dynamicABIs)
		if err != nil {
			return nil, err
		}
		out.WriteString(contracts)
	}
	chainFields := abi.GoFieldNames(app.chainNames)
	for _, name := range app.chainNames {
		typ := "Chain" + chainFields[name]
		fmt.Fprintf(&out, "type %s struct{}\nfunc (%s) Name()string{return %q}\nfunc (%s) ID()uint64{return %d}\n", typ, typ, name, typ, app.ids[name])
	}
	w := chainBindingWriter{app, chainFields, map[string]chainGroup{}}
	var all []string
	for _, name := range app.chainNames {
		if app.contextChains[name] {
			all = append(all, name)
		}
	}
	if group := w.write(&out, all); group.typ != "" {
		fmt.Fprintf(&out, "func ResolveChain(ctx *indexing.Context)(%s,error){return %s(ctx)}\n", group.typ, group.resolver)
	}
	var nameTypes []string
	for _, source := range app.sources {
		group := w.write(&out, source.chains)
		for _, event := range source.events {
			name := event.name
			typ := "Name" + fields[name]
			nameTypes = append(nameTypes, typ)
			fmt.Fprintf(&out, "type %s struct{}\nfunc (%s) String()string{return %q}\nfunc (%s) isEventName(){}\n", typ, typ, name, typ)
			fmt.Fprintf(&out, "func (n %s) IndexingFunction(fn indexing.Handler)config.SourceIndexingFunction{return config.SourceIndexingFunction{Name:n.String(),Fn:fn}}\n", typ)
			if group.typ != "" {
				if registry {
					writeContextCallback(&out, fields[name], event, group.typ)
				}
				fmt.Fprintf(&out, "func (%s) Chain(ctx *indexing.Context)(%s,error){return %s(ctx)}\n", typ, group.typ, group.resolver)
			}
		}
	}
	out.WriteString("type EventNames interface{")
	if len(nameTypes) == 0 {
		out.WriteString("int;string")
	} else if len(nameTypes) <= 100 {
		out.WriteString(strings.Join(nameTypes, " | "))
	} else {
		out.WriteString("isEventName()")
	}
	out.WriteString("}\n")
	out.WriteString(payloads.String())
	return formatApplicationBindings(pkg, out.String())
}

// Read only selectors from the generated AST so an ABI literal that contains
// a package name cannot create a spurious import.
func formatApplicationBindings(pkg, body string) ([]byte, error) {
	header := "// Code generated by ponder-go codegen; DO NOT EDIT.\npackage " + pkg + "\n"
	file, err := parser.ParseFile(token.NewFileSet(), "bindings.go", header+body, 0)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if selector, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := selector.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	var out strings.Builder
	out.WriteString(header)
	for _, pkg := range []struct{ name, path string }{{"context", "context"}, {"fmt", "fmt"}, {"big", "math/big"}, {"abi", "ponder.local/ponder-go/abi"}, {"config", "ponder.local/ponder-go/config"}, {"indexing", "ponder.local/ponder-go/indexing"}} {
		if used[pkg.name] {
			fmt.Fprintf(&out, "import %q\n", pkg.path)
		}
	}
	out.WriteString(body)
	return format.Source([]byte(out.String()))
}

type chainGroup struct{ typ, resolver string }
type chainBindingWriter struct {
	app    *bindingApplication
	fields map[string]string
	groups map[string]chainGroup
}

// Sources with the same eligible chains share one type and resolver.
func (w *chainBindingWriter) write(out *strings.Builder, selected []string) chainGroup {
	if len(selected) == 0 {
		return chainGroup{}
	}
	member := map[string]bool{}
	for _, name := range selected {
		member[name] = true
	}
	var types []string
	for _, name := range w.app.chainNames {
		if member[name] {
			types = append(types, "Chain"+w.fields[name])
		}
	}
	key := strings.Join(types, "|")
	if group, ok := w.groups[key]; ok {
		return group
	}
	index := strconv.Itoa(len(w.groups) + 1)
	typ := "ChoiceOfChains" + index
	zero := "nil"
	if len(types) == 1 {
		typ = types[0]
		zero = typ + "{}"
	} else {
		method := "isChainChoice" + index
		fmt.Fprintf(out, "type %s interface{Name()string; ID()uint64; %s()}\n", typ, method)
		for _, item := range types {
			fmt.Fprintf(out, "func (%s) %s(){}\n", item, method)
		}
	}
	resolve := "resolveChain" + index
	fmt.Fprintf(out, "func %s(ctx *indexing.Context)(%s,error){name,id,err:=ctx.ChainIdentity(); if err!=nil{return %s,err}; switch name {\n", resolve, typ, zero)
	for _, name := range w.app.chainNames {
		if member[name] {
			fmt.Fprintf(out, "case %q: if id==%d{return Chain%s{},nil}\n", name, w.app.ids[name], w.fields[name])
		}
	}
	fmt.Fprintf(out, "}; return %s,fmt.Errorf(\"unexpected callback chain %%q (%%d)\",name,id)}\n", zero)
	fmt.Fprintf(out, "type MembersOf%s interface{", typ)
	if len(types) <= 100 {
		out.WriteString(key)
	} else {
		out.WriteString(typ)
	}
	out.WriteString("}\n")
	group := chainGroup{typ, resolve}
	w.groups[key] = group
	return group
}
