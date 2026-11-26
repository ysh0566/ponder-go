// The Go CLI launches native Go apps. It does not execute TS config/handler files.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/logging"
	"github.com/ysh0566/ponder-go/orm"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/values"
	"go/format"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const version = "0.18.2-go.0"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := run(ctx, os.Args[1:], os.Stdout); e != nil {
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out io.Writer) error {
	return runCLI(ctx, args, out, os.Stderr)
}

func runCommand(ctx context.Context, args []string, out, diagnostics io.Writer, log *logging.Logger) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(out, "ponder-go "+version+" (local port in progress)\n\nCommands: init, dev, start, serve, codegen, db list, db create-views, db prune, checkpoint encode/decode\nUse dev, start, or serve with --root, --schema, --hostname, and --port. Use dev --disable-ui for plain logs.\nGlobal logging options: --log-level LEVEL, --log-format pretty|json, -v/--debug, -vv/--trace.")
		return nil
	}
	switch args[0] {
	case "--version", "-V":
		fmt.Fprintln(out, version)
		return nil
	case "init":
		flags := flag.NewFlagSet("init", flag.ContinueOnError)
		flags.SetOutput(log.Diagnostics())
		template := flags.String("template", "go", "Project template (go, erc20, erc721, erc1155, erc4626, factory, or typescript)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return fmt.Errorf("usage: ponder init [--template go|erc20|erc721|erc1155|erc4626|factory|typescript] <new-project-directory>")
		}
		return createProject(ctx, *template, flags.Arg(0), out)
	case "checkpoint":
		if len(args) < 2 {
			return fmt.Errorf("checkpoint encode|decode required")
		}
		if args[1] == "encode" {
			var cp checkpoint.Checkpoint
			if e := json.NewDecoder(os.Stdin).Decode(&cp); e != nil {
				return e
			}
			s, e := checkpoint.Encode(cp)
			if e != nil {
				return e
			}
			fmt.Fprintln(out, s)
			return nil
		}
		if args[1] == "decode" {
			raw, e := io.ReadAll(os.Stdin)
			if e != nil {
				return e
			}
			cp, e := checkpoint.Decode(strings.TrimSpace(string(raw)))
			if e != nil {
				return e
			}
			return json.NewEncoder(out).Encode(cp)
		}
		return fmt.Errorf("unknown checkpoint action")
	case "start", "serve", "dev":
		return launchProject(ctx, args[0], args[1:], out, diagnostics, log)
	case "codegen":
		flags := flag.NewFlagSet("codegen", flag.ContinueOnError)
		flags.SetOutput(log.Diagnostics())
		input := flags.String("schema", "ponder.schema.json", "Native Go schema JSON")
		output := flags.String("out", "ponder_generated.go", "Output Go model file")
		packageName := flags.String("package", "main", "Go package")
		typed := flags.Bool("typed", false, "Generate typed native ORM model/key/insert/update APIs")
		callbackDB := flags.Bool("callback-db", false, "Generate transaction-bound tables for a typed callback context")
		catalog := flags.String("abi", "", "Contract ABI JSON for typed event and function names")
		application := flags.String("app-config", "", "Source configuration JSON for callback names and chains")
		registry := flags.Bool("registry", false, "Generate handlers with typed chain, contracts and database context")
		eventTypes := flags.Bool("event-types", false, "Include typed callback events with --app-config")
		var dynamicABIs []string
		flags.Func("dynamic-abi", "Contract with a runtime ABI (repeatable with --app-config)", func(name string) error { dynamicABIs = append(dynamicABIs, name); return nil })
		var mergeFiles []string
		flags.Func("merge-abi", "ABI file to include in an ordered merge (repeatable)", func(file string) error { mergeFiles = append(mergeFiles, file); return nil })
		bigint := flags.String("bigint-shape", "", "Go type expression with BigInt leaves for a typed replacer")
		parameters := flags.String("abi-parameters", "", "ABI parameter JSON for a typed decoder")
		decoderName := flags.String("decoder", "Parameters", "Exported ABI decoder name")
		if e := flags.Parse(args[1:]); e != nil {
			return e
		}
		if !schema.Identifier(*packageName) {
			return fmt.Errorf("invalid package name")
		}
		if *callbackDB {
			if *application != "" || *catalog != "" || *bigint != "" || *parameters != "" || len(mergeFiles) > 0 || *eventTypes || *registry || len(dynamicABIs) > 0 {
				return fmt.Errorf("--callback-db requires schema generation")
			}
			*typed = true
		}
		if *application != "" {
			schemaFlag := false
			flags.Visit(func(f *flag.Flag) {
				if f.Name == "schema" {
					schemaFlag = true
				}
			})
			if schemaFlag || len(mergeFiles) > 0 || *bigint != "" || *catalog != "" || *parameters != "" || *typed {
				return fmt.Errorf("choose a single code generation mode")
			}
			data, err := os.ReadFile(*application)
			if err != nil {
				return err
			}
			cfg, err := config.ParseSourceConfiguration(data)
			if err != nil {
				return err
			}
			generator := config.GenerateApplicationBindings
			if *eventTypes {
				generator = config.GenerateApplicationEventBindings
			}
			if *registry {
				generator = config.GenerateApplicationRegistry
			}
			src, err := generator(*packageName, cfg, dynamicABIs...)
			if err != nil {
				return err
			}
			return os.WriteFile(*output, src, 0644)
		}
		if *registry {
			return fmt.Errorf("--registry requires --app-config")
		}
		if *eventTypes {
			return fmt.Errorf("--event-types requires --app-config")
		}
		if len(dynamicABIs) > 0 {
			return fmt.Errorf("--dynamic-abi requires --app-config")
		}
		if len(mergeFiles) > 0 || *bigint != "" {
			if *catalog != "" || *parameters != "" || *typed || (len(mergeFiles) > 0 && *bigint != "") {
				return fmt.Errorf("choose a single code generation mode")
			}
			var source []byte
			var err error
			if len(mergeFiles) > 0 {
				var inputs []json.RawMessage
				for _, file := range mergeFiles {
					data, e := os.ReadFile(file)
					if e != nil {
						return e
					}
					inputs = append(inputs, data)
				}
				source, err = abi.GenerateMergedCatalog(*packageName, inputs...)
			} else {
				source, err = values.GenerateBigIntReplacer(*packageName, "ReplaceBigInts", *bigint)
			}
			if err != nil {
				return err
			}
			return os.WriteFile(*output, source, 0644)
		}
		if *catalog != "" {
			if *parameters != "" || *typed {
				return fmt.Errorf("choose one ABI catalog, parameter decoder, or ORM model")
			}
			raw, err := os.ReadFile(*catalog)
			if err != nil {
				return err
			}
			source, err := abi.GenerateCatalog(*packageName, raw)
			if err != nil {
				return err
			}
			return os.WriteFile(*output, source, 0644)
		}
		if *parameters != "" {
			raw, e := os.ReadFile(*parameters)
			if e != nil {
				return e
			}
			var params []abi.Parameter
			if e = json.Unmarshal(raw, &params); e != nil {
				return e
			}
			source, e := abi.GenerateParameters(*packageName, *decoderName, params)
			if e != nil {
				return e
			}
			return os.WriteFile(*output, source, 0644)
		}
		raw, e := os.ReadFile(*input)
		if e != nil {
			return e
		}
		var model schema.Schema
		if e = json.Unmarshal(raw, &model); e != nil {
			return e
		}
		if e = model.Validate(schema.Multichain); e != nil {
			return e
		}
		if *typed {
			generate := orm.GenerateModels
			if *callbackDB {
				generate = orm.GenerateCallbackDatabase
			}
			source, e := generate(*packageName, model)
			if e != nil {
				return e
			}
			return os.WriteFile(*output, source, 0644)
		}
		var source strings.Builder
		fmt.Fprintf(&source, "// Code generated by ponder-go codegen; DO NOT EDIT.\npackage %s\n", *packageName)
		big := false
		for _, t := range model.Tables {
			for _, c := range t.Columns {
				big = big || c.Type == "bigint" || c.Type == "bigint[]"
			}
		}
		if big {
			source.WriteString("import \"math/big\"\n")
		}
		for _, t := range model.Tables {
			fmt.Fprintf(&source, "type %s struct {\n", goName(t.Name))
			for _, c := range t.Columns {
				typ := "any"
				switch strings.TrimSuffix(c.Type, "[]") {
				case "text", "hex", "address", "uuid", "numeric", "date", "timestamp":
					typ = "string"
				case "bigint":
					typ = "*big.Int"
				case "int8", "integer", "smallint":
					typ = "int64"
				case "boolean":
					typ = "bool"
				case "real", "doublePrecision":
					typ = "float64"
				case "bytes":
					typ = "[]byte"
				}
				if strings.HasSuffix(c.Type, "[]") {
					typ = "[]" + typ
				}
				fmt.Fprintf(&source, "%s %s `json:\"%s\"`\n", goName(c.Name), typ, c.Name)
			}
			source.WriteString("}\n")
		}
		formatted, e := format.Source([]byte(source.String()))
		if e != nil {
			return e
		}
		if e = os.WriteFile(*output, formatted, 0644); e != nil {
			return e
		}
		fmt.Fprintln(out, *output)
		return nil
	case "db":
		return manageDatabase(ctx, args[1:], out, log.Diagnostics())
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func goName(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
