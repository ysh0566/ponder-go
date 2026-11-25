package logging

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRuntimeLogRecords(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  []float64
	}{
		{"trace", []float64{10, 20, 30, 40, 50}}, {"debug", []float64{20, 30, 40, 50}},
		{"info", []float64{30, 40, 50}}, {"warn", []float64{40, 50}}, {"error", []float64{50}}, {"silent", nil},
	} {
		var buf strings.Builder
		log, err := New(&buf, Settings{Level: tc.level, Format: "json"})
		if err != nil {
			t.Fatal(err)
		}
		log.Trace("checkpoint", "chain", 1)
		fields := map[string]any{"msg": "changed range", "range": 10}
		log.Debug(fields)
		log.Info("ready")
		log.Warn(map[string]any{"msg": "retry", "error": errors.New("interrupted")})
		log.Error("failed")
		if len(fields) != 2 || fields["msg"] != "changed range" {
			t.Fatal("input fields mutated")
		}
		dec := json.NewDecoder(strings.NewReader(buf.String()))
		for _, want := range tc.want {
			var record map[string]any
			if err := dec.Decode(&record); err != nil || record["level"] != want {
				t.Fatal(tc.level, record, err)
			}
			if stamp, ok := record["time"].(float64); !ok || stamp <= 0 {
				t.Fatal(record)
			}
			if want == 40 && record["error"] != "interrupted" {
				t.Fatal(record)
			}
			if want == 20 && record["range"] != float64(10) {
				t.Fatal(record)
			}
		}
		if dec.More() {
			t.Fatalf("%s unexpected extra log: %s", tc.level, buf.String())
		}
	}
	var buf strings.Builder
	pretty, err := New(&buf, Settings{Level: "trace", Format: "pretty"})
	if err != nil {
		t.Fatal(err)
	}
	pretty.Trace("checkpoint")
	if !strings.Contains(buf.String(), "TRACE") || !strings.Contains(buf.String(), "checkpoint") {
		t.Fatal(buf.String())
	}
}
