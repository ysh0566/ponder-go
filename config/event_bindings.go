package config

import (
	"fmt"
	"strings"

	"ponder.local/ponder-go/abi"
)

type bindingField struct {
	name, typ, expression string
}

func writeBindingEvent(out *strings.Builder, field string, e bindingEvent) error {
	fields := []bindingField{{"ID", "string", "v.EventID()"}, {"Block", "indexing.EventBlock", "v.BlockFields()"}}
	if e.kind != "block" {
		fields = append(fields, bindingField{"Transaction", "indexing.EventTransaction", "v.TransactionFields()"})
	}
	switch e.kind {
	case "log":
		fields = append(fields, bindingField{"Log", "indexing.EventLog", "v.LogFields()"})
	case "trace", "transfer":
		fields = append(fields, bindingField{"Trace", "indexing.EventTrace", "v.TraceFields()"})
	}
	switch e.receipt {
	case optionalReceipt:
		fields = append(fields, bindingField{"TransactionReceipt", "*indexing.EventReceipt", "v.OptionalReceipt()"})
	case requiredReceipt:
		fields = append(fields, bindingField{"TransactionReceipt", "indexing.EventReceipt", "v.ReceiptFields()"})
	}
	if e.kind == "transfer" {
		fields = append(fields, bindingField{"Transfer", "indexing.EventTransfer", `abi.ProjectDecoded[indexing.EventTransfer](v.Get("transfer"))`})
	}
	project := func(name, typ string) {
		key := strings.ToLower(name)
		expr := ""
		switch typ {
		case "abi.NoValue":
			expr = fmt.Sprintf("v.AbsentABIValue(%q)", key)
		case "[0]struct{}":
			expr = "v.EmptyLogArgs()"
		default:
			expr = fmt.Sprintf("abi.ProjectDecoded[%s](v.Get(%q))", typ, key)
		}
		fields = append(fields, bindingField{name, typ, expr})
	}
	if e.kind == "log" || e.kind == "trace" {
		args, err := abi.CallbackArgsType(e.inputs, e.kind == "log")
		if err != nil {
			return err
		}
		project("Args", args)
	}
	if e.kind == "trace" {
		result, err := abi.CallbackResultType(e.outputs)
		if err != nil {
			return err
		}
		project("Result", result)
	}
	typ := "Event" + field
	fmt.Fprintf(out, "type %s struct{\n", typ)
	for _, f := range fields {
		fmt.Fprintf(out, "%s %s\n", f.name, f.typ)
	}
	fmt.Fprintf(out, "}\nfunc (Name%s) Event(v indexing.EventView)(%s,error){var event %s; var err error\n", field, typ, typ)
	for _, f := range fields {
		fmt.Fprintf(out, "event.%s,err=%s; if err!=nil{return %s{},err}\n", f.name, f.expression, typ)
	}
	fmt.Fprintf(out, "return event,nil}\nfunc (n Name%s) On(fn func(context.Context,%s,*indexing.Context)error)config.SourceIndexingFunction{return n.IndexingFunction(indexing.HandleEvent(n.Event,fn))}\n", field, typ)
	return nil
}
