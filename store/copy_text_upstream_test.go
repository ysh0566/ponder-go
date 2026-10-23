package store

import (
	"ponder.local/ponder-go/schema"
	"testing"
)

func TestUpstreamCopyText(t *testing.T) {
	table := schema.Table{Name: "test", Columns: []schema.Column{{Name: "bytes", Type: "bytes", Primary: true}}}
	actual, e := GetCopyText(table, []Row{{"bytes": []byte{0, 128, 255, 1}}, {"bytes": []byte{}}, {"bytes": []byte{0x5c, 0x4e, 9, 10, 13}}})
	if e != nil {
		t.Fatal(e)
	}
	if actual != "\\\\x0080ff01\n\\\\x\n\\\\x5c4e090a0d" {
		t.Fatalf("source toBe byte COPY text: %q", actual)
	}
	table.Columns = append(table.Columns, schema.Column{Name: "text", Type: "text", NotNull: true})
	actual, e = GetCopyText(table, []Row{{"bytes": []byte{0x5c, 0x4e, 9, 10, 13}, "text": "ok"}})
	if e != nil {
		t.Fatal(e)
	}
	if actual != "\\\\x5c4e090a0d\tok" {
		t.Fatalf("source toBe multicolumn COPY text: %q", actual)
	}
}
