package config

import "testing"

// A zero-valued selection is representable in Go. The safe native consumer
// must return an error rather than panic while inspecting its interface field.
func TestTypedSourceMissingSelection(t *testing.T) {
	base, err := ParseSourceConfiguration([]byte(`{"chains":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	typed := CreateTypedSourceConfiguration(base, "a", SourceTypedContract[SourceUndefinedBound, SourceUndefinedBound]{ABI: []byte(`[]`)})
	built, err := BuildTypedSourceConfig(typed, SourceBuildOptions{})
	if err == nil || built != nil {
		t.Fatal("missing typed chain selection must reject before creating clients", built, err)
	}
}
