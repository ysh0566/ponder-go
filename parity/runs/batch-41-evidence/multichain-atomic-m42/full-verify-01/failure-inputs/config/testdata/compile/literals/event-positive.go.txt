package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"ponder.local/ponder-go/config"
	bindings "ponder.local/ponder-go/config/testdata/sourcebindings"
	"strings"
)

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	base, err := config.ParseSourceConfiguration(raw)
	if err != nil {
		panic(err)
	}
	input := base.Value()["contracts"].(map[string]any)["a"].(map[string]any)
	_ = strings.Contains
	typed, err := bindings.Create(base, "a", bindings.Contract[config.SourceUndefinedBound, config.SourceUndefinedBound]{Chain: bindings.Chains.Mainnet, Filter: &bindings.Filter{Event: bindings.Events.Event0, Args: input["filter"].(map[string]any)["args"]}})
	if err != nil {
		panic(err)
	}
	built, err := config.BuildTypedSourceConfig(typed, config.SourceBuildOptions{})
	if err != nil {
		panic(err)
	}
	indexing, err := config.SafeBuildTypedSourceIndexing(context.Background(), typed, built, []config.SourceIndexingFunction{{Name: "a:Event0", Fn: func() {}}})
	_ = indexing
	if input["filter"].(map[string]any)["args"].(map[string]any)["arg"] != "0x" {
		panic("original short byte arg changed")
	}
	if err == nil || !strings.Contains(err.Error(), "bytes argument size must be 32") {
		panic(fmt.Sprintf("expected remaining runtime byte-size error, got %v", err))
	}
	fmt.Println("valid generated event reached expected runtime bytes32 validation")
}
