package events_test

import (
	"ponder.local/ponder-go/events"
	"testing"
)

func TestDecodedPayloadKinds(t *testing.T) {
	block := events.Object{"number": 1}
	tx := events.Object{"from": "alice"}
	data := events.Object{"id": "id", "block": block, "transaction": tx, "transactionReceipt": events.Object{"status": "ok"}, "trace": events.Object{"type": "CALL"}, "transfer": events.Object{"value": 3}, "result": "value", "args": "inputs", "log": events.Object{"data": "0x"}}
	for _, kind := range []string{"block", "transaction", "log", "trace", "transfer"} {
		p, err := events.PayloadOf(events.Object{"type": kind, "event": data})
		if err != nil {
			t.Fatal(err)
		}
		if p.ID() != "id" || p.Block()["number"] != 1 {
			t.Fatal("missing common metadata")
		}
		switch v := p.(type) {
		case events.BlockPayload:
			if kind != "block" {
				t.Fatal(kind)
			}
		case events.TransactionPayload:
			if kind != "transaction" || v.Transaction()["from"] != "alice" || v.Receipt()["status"] != "ok" {
				t.Fatal(kind)
			}
		case events.LogPayload:
			if kind != "log" || v.Log()["data"] != "0x" || v.Args() != "inputs" {
				t.Fatal(kind)
			}
		case events.TracePayload:
			if kind != "trace" || v.Args() != "inputs" || v.Result() != "value" || v.Trace()["type"] != "CALL" {
				t.Fatal(kind)
			}
		case events.TransferPayload:
			if kind != "transfer" || v.Transfer()["value"] != 3 || v.Trace()["type"] != "CALL" {
				t.Fatal(kind)
			}
		default:
			t.Fatalf("unexpected %T", p)
		}
	}
	for _, v := range []events.Object{{"type": "log"}, {"type": "setup", "event": data}} {
		if _, err := events.PayloadOf(v); err == nil {
			t.Fatal("accepted malformed envelope")
		}
	}
}
