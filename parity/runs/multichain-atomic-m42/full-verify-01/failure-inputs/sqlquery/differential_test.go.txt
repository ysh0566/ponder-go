package sqlquery

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	pgquery "github.com/pganalyze/pg_query_go/v5"
)

type sourceSQLResult struct {
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
	Error *struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

// TestSQLSourceDifferential is supplementary actual-source evidence only.
func TestSQLSourceDifferential(t *testing.T) {
	raw, err := os.ReadFile("../testdata/sql/source-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version int `json:"source_parser_version"`
		Cases   []struct {
			ID               string          `json:"id"`
			SQL              string          `json:"sql"`
			Readonly         bool            `json:"readonly"`
			FirstValidation  sourceSQLResult `json:"first_validation"`
			CachedValidation sourceSQLResult `json:"cached_validation"`
			Relations        sourceSQLResult `json:"relations"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	parsed, err := pgquery.ParseToJSON("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	var native struct{ Version int }
	if err = json.Unmarshal([]byte(parsed), &native); err != nil || native.Version != fixture.Version {
		t.Fatalf("Actual parser versions native=%d source=%d error=%v", native.Version, fixture.Version, err)
	}
	validator := NewValidator()
	for _, original := range fixture.Cases {
		t.Run(original.ID, func(t *testing.T) {
			if readonly := IsReadonly(original.SQL); readonly != original.Readonly {
				t.Errorf("Readonly native=%v source=%v", readonly, original.Readonly)
			}
			check := func(name string, expected sourceSQLResult, value any, actualErr error) {
				t.Helper()
				if !expected.OK {
					if expected.Error == nil || actualErr == nil || expected.Error.Message != actualErr.Error() {
						t.Errorf("%s: native error=%v source=%+v", name, actualErr, expected.Error)
					}
					return
				}
				if actualErr != nil {
					t.Errorf("%s: %v", name, actualErr)
					return
				}
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err = json.Unmarshal(encoded, &got); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(expected.Value, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Error(fmt.Sprintf("%s: native=%s source=%s", name, encoded, expected.Value))
				}
			}
			check("first validation", original.FirstValidation, nil, validator.Validate(original.SQL))
			check("cached validation", original.CachedValidation, nil, validator.Validate(original.SQL))
			relations, err := validator.Relations(original.SQL)
			var names []string
			if err == nil {
				names = relations.Values()
			}
			check("relations", original.Relations, names, err)
		})
	}
}
