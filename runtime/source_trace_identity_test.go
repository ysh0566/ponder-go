package runtime

import (
	"bytes"
	"encoding/json"
	"os"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"testing"
)

func TestSourceTraceIdentityVectors(t *testing.T) {
	file := os.Getenv("PONDER_M34_TRACE_FIXTURE")
	if file == "" {
		file = "../testdata/rpc/source-trace-identity.json"
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name    string            `json:"name"`
			Input   any               `json:"input"`
			Request rpc.ActionRequest `json:"request"`
			Result  any               `json:"result"`
			Error   *struct {
				Name    string   `json:"name"`
				Message string   `json:"message"`
				Meta    []string `json:"meta"`
			} `json:"error"`
			After any `json:"after"`
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 12 {
		t.Fatal("complete actual source trace vectors required")
	}
	for _, vector := range fixture.Cases {
		t.Run(vector.Name, func(t *testing.T) {
			registry := newSourceLiveHistoricalRegistry()
			restored, err := registry.restore(vector.Input)
			if err != nil {
				t.Fatal(err)
			}
			rows, ok := restored.([]any)
			if !ok {
				t.Fatal("source trace input array required")
			}
			input := []filter.Object{}
			for _, row := range rows {
				object, ok := row.(filter.Object)
				if !ok {
					t.Fatal("source trace response object required")
				}
				input = append(input, object)
			}
			actual, err := rpc.FlattenSourceCallTraces(input, vector.Request)
			if vector.Error != nil {
				if err == nil {
					t.Fatal("source trace validation error missing")
				}
				provider, ok := err.(*rpc.ProviderError)
				if !ok || vector.Error.Name != "RpcProviderError" || provider.Message != vector.Error.Message {
					t.Fatalf("specific source error differs: %T %v, source %+v", err, err, vector.Error)
				}
				if len(provider.Meta) != len(vector.Error.Meta) {
					t.Fatal("source request metadata length differs", provider.Meta, vector.Error.Meta)
				}
				for i, value := range provider.Meta {
					if value != vector.Error.Meta[i] {
						t.Fatal("source request metadata differs", provider.Meta, vector.Error.Meta)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				result := []filter.Object{}
				for _, trace := range actual {
					result = append(result, filter.Object{"transactionHash": trace.TransactionHash, "trace": trace.Trace})
				}
				if err := registry.compare(result, vector.Result, "actual source trace result/frame identities"); err != nil {
					t.Fatal(err)
				}
			}
			if err := registry.compare(input, vector.After, "actual source provider input mutation/order"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
