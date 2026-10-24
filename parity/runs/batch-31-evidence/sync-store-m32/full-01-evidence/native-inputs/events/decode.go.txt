package events

import "fmt"

// DecodePublicEvents projects block/account events. Contract ABI decoding stays
// explicitly unsupported here until its original fixtures are ported.
func DecodePublicEvents(chain Object, callbacks []Object, raw []RawEvent) ([]Object, error) {
	out := []Object{}
	for _, e := range raw {
		if e.EventCallbackIndex < 0 || e.EventCallbackIndex >= len(callbacks) {
			return nil, fmt.Errorf("invalid event callback index")
		}
		callback := callbacks[e.EventCallbackIndex]
		kind := text(object(callback["filter"])["type"])
		body := Object{"id": e.Checkpoint, "block": e.Block}
		eventType := ""
		if callback["type"] == "block" {
			eventType = "block"
		} else if callback["type"] == "account" && (kind == "transaction" || kind == "transfer") {
			eventType = kind
			body["transaction"] = optionalObject(e.Transaction)
			body["transactionReceipt"] = optionalObject(e.TransactionReceipt)
			if kind == "transfer" {
				if e.Trace == nil {
					return nil, fmt.Errorf("transfer event missing trace")
				}
				body["trace"] = e.Trace
				body["transfer"] = copyFields(e.Trace, "from", "to", "value")
			}
		} else {
			return nil, fmt.Errorf("unsupported public event type %v/%s", callback["type"], kind)
		}
		out = append(out, Object{"type": eventType, "checkpoint": e.Checkpoint, "chain": chain, "eventCallback": callback, "event": body})
	}
	return out, nil
}
