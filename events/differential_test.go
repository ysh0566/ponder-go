package events

import (
	"encoding/json"
	"fmt"
	"os"
	"ponder.local/ponder-go/abi"
	"testing"
)

func TestEventBuildDifferential(t *testing.T) {
	raw, e := os.ReadFile("../testdata/runtime/event-build-differential.json")
	if e != nil {
		t.Fatal(e)
	}
	var data struct {
		Vectors []struct {
			Operation    string
			Args, Result any
			Error        *struct{ Name, Message string }
		}
	}
	if e = json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	for i, v := range data.Vectors {
		t.Run(fmt.Sprintf("%03d_%s", i, v.Operation), func(t *testing.T) {
			a := object(fixture(v.Args))
			var got any
			var e error
			switch v.Operation {
			case "decodeEvents":
				collector := &decodeCollector{logs: []Object{}}
				decoder := Decoder{Logger: collector}
				rawEvents := []RawEvent{}
				for _, value := range objects(a["rawEvents"]) {
					rawEvents = append(rawEvents, rawObject(value))
				}
				decoded, err := decoder.DecodeEvents(object(a["chain"]), objects(a["callbacks"]), rawEvents)
				e = err
				got = Object{"events": decoded, "logs": collector.logs}
			case "functionMetadata":
				var item abi.Function
				e = metadata(a["item"], &item)
				got = Object{"selector": abi.FunctionSelector(item), "safeName": item.SafeName(a["overloaded"] == true)}
			case "buildEvents":
				got, e = BuildEvents(batch(a))
			case "syncBlockToInternal":
				got, e = SyncBlockToInternal(object(a["block"]))
			case "syncLogToInternal":
				got, e = SyncLogToInternal(object(a["log"]))
			case "syncTransactionToInternal":
				got, e = SyncTransactionToInternal(object(a["transaction"]))
			case "syncTransactionReceiptToInternal":
				got, e = SyncTransactionReceiptToInternal(object(a["transactionReceipt"]))
			case "syncTraceToInternal":
				got, e = SyncTraceToInternal(object(a["trace"]), object(a["block"]), object(a["transaction"]))
			default:
				t.Fatal(v.Operation)
			}
			if v.Error != nil {
				if e == nil {
					t.Fatal("source rejects", got)
				}
				if v.Operation == "buildEvents" && v.Error.Name == "Error" && e.Error() != v.Error.Message {
					t.Fatal("source error text", e, v.Error.Message)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			actual, e := json.Marshal(marked(got))
			if e != nil {
				t.Fatal(e)
			}
			expected, e := json.Marshal(v.Result)
			if e != nil {
				t.Fatal(e)
			}
			if string(actual) != string(expected) {
				t.Fatalf("%s\ngot %s\nwant %s", v.Operation, actual, expected)
			}
		})
	}
}

type decodeCollector struct{ logs []Object }

func (c *decodeCollector) Debug(fields Object) { c.logs = append(c.logs, fields) }
