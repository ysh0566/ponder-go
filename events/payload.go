package events

import "fmt"

// Payload is the common part of the five decoded event bodies. Type switches
// expose fields belonging to an individual event kind. All views retain the
// decoded objects so selecting a field preserves its identity.
type Payload interface {
	ID() string
	Block() Object
	eventPayload()
}

type BlockPayload struct{ body Object }

func (p BlockPayload) ID() string    { return text(p.body["id"]) }
func (p BlockPayload) Block() Object { return object(p.body["block"]) }
func (BlockPayload) eventPayload()   {}

type TransactionPayload struct{ BlockPayload }

func (p TransactionPayload) Transaction() Object { return object(p.body["transaction"]) }
func (p TransactionPayload) Receipt() Object     { return object(p.body["transactionReceipt"]) }

type LogPayload struct{ TransactionPayload }

func (p LogPayload) Args() any   { return get(p.body, "args") }
func (p LogPayload) Log() Object { return object(p.body["log"]) }

type TracePayload struct{ TransactionPayload }

func (p TracePayload) Args() any     { return get(p.body, "args") }
func (p TracePayload) Result() any   { return get(p.body, "result") }
func (p TracePayload) Trace() Object { return object(p.body["trace"]) }

type TransferPayload struct{ TransactionPayload }

func (p TransferPayload) Transfer() Object { return object(p.body["transfer"]) }
func (p TransferPayload) Trace() Object    { return object(p.body["trace"]) }

// PayloadOf narrows the envelope returned by Decoder.DecodeEvents.
func PayloadOf(envelope Object) (Payload, error) {
	body := object(envelope["event"])
	if body == nil {
		return nil, fmt.Errorf("decoded event body is missing")
	}
	block := BlockPayload{body: body}
	tx := TransactionPayload{BlockPayload: block}
	switch envelope["type"] {
	case "block":
		return block, nil
	case "transaction":
		return tx, nil
	case "log":
		return LogPayload{tx}, nil
	case "trace":
		return TracePayload{tx}, nil
	case "transfer":
		return TransferPayload{tx}, nil
	default:
		return nil, fmt.Errorf("unknown decoded event type %v", envelope["type"])
	}
}

// DecodePayloads runs the ordinary ABI decoder and returns its successful
// events in source order with a statically narrowable payload.
func (d *Decoder) DecodePayloads(chain Object, callbacks []Object, raw []RawEvent) ([]Payload, error) {
	envelopes, err := d.DecodeEvents(chain, callbacks, raw)
	if err != nil {
		return nil, err
	}
	payloads := make([]Payload, len(envelopes))
	for i, envelope := range envelopes {
		payloads[i], err = PayloadOf(envelope)
		if err != nil {
			return nil, err
		}
	}
	return payloads, nil
}
