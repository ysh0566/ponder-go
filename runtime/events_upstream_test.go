package runtime

import (
	"reflect"
	"testing"
)

func TestUpstreamSplitEvents(t *testing.T) {
	// Original numeric timestamp and bigint number use native RPC hex fields;
	// all other fixture values and every full inline-snapshot field are retained.
	events := []OrderedEvent{
		{ChainID: 1, Checkpoint: "0", Event: Event{Block: Block{Hash: "0x1", Timestamp: "0x1", Number: "0x1"}}},
		{ChainID: 1, Checkpoint: "0", Event: Event{Block: Block{Hash: "0x2", Timestamp: "0x2", Number: "0x2"}}},
	}
	result, e := SplitEvents(events)
	if e != nil {
		t.Fatal(e)
	}
	want := []EventGroup{
		{ChainID: 1, Checkpoint: "000000000100000000000000010000000000000001999999999999999999999999999999999", Events: []OrderedEvent{{ChainID: 1, Checkpoint: "0", Event: Event{Block: Block{Hash: "0x1", Number: "0x1", Timestamp: "0x1"}}}}},
		{ChainID: 1, Checkpoint: "000000000200000000000000010000000000000002999999999999999999999999999999999", Events: []OrderedEvent{{ChainID: 1, Checkpoint: "0", Event: Event{Block: Block{Hash: "0x2", Number: "0x2", Timestamp: "0x2"}}}}},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("source full inline snapshot: %#v\nwant %#v", result, want)
	}
}
