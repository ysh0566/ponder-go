package indexing

import (
	"context"
	"testing"
)

type userNamedError struct{ message string }

func (e *userNamedError) Error() string     { return e.message }
func (e *userNamedError) ErrorName() string { return "UniqueConstraintError" }
func (e *userNamedError) Retryable() bool   { return true }

type userExtendedBase struct{ BaseError }

func (e *userExtendedBase) ErrorName() string { return "UserExtendedBase" }
func TestDatabaseErrorBrandNotDisplayName(t *testing.T) {
	ordinary := &userNamedError{message: "ECONNRESET"}
	parsed := ParseDatabaseError(ordinary)
	domain, ok := parsed.(*DatabaseError)
	if !ok || domain.Name != "DbConnectionError" || domain.Cause != ordinary {
		t.Fatalf("display traits bypassed parsing: %T %v", parsed, parsed)
	}
	known := &userExtendedBase{BaseError{Message: "ECONNRESET"}}
	if ParseDatabaseError(known) != known {
		t.Fatal("explicit BaseError inheritance lost identity")
	}
	state := &ErrorState{}
	db := NewDatabase(nil, state)
	if e := db.capture(&userNamedError{message: "ordinary"}); e == nil || state.GetRetryableError() != nil {
		t.Fatal("arbitrary retryable trait became sticky")
	}
}
func TestServiceErrorBrandNotDisplayName(t *testing.T) {
	serviceSemanticStore(t, func(t *testing.T, db *Database, state *ErrorState) {
		for _, known := range []bool{false, true} {
			var thrown error = &userNamedError{message: "ordinary"}
			if known {
				thrown = &userExtendedBase{BaseError{Message: "known"}}
			}
			options, cb, _ := semanticOptions(t, db, state, Handler(func(context.Context, *HandlerArgs) error { return thrown }))
			service := mustSemanticService(t, options)
			err := service.ProcessRealtimeEvents(context.Background(), []Object{semanticEvent(options.Build.Chains[0], cb, 2)})
			if known {
				if err != thrown {
					t.Fatal("known domain error identity lost")
				}
			} else {
				if wrapped, ok := err.(*IndexingFunctionError); !ok || wrapped.Message != "ordinary" {
					t.Fatalf("ordinary named error not wrapped: %T %v", err, err)
				}
			}
			if !service.Metrics().HasError || service.EventCount()["Token:Transfer"] != 0 {
				t.Fatal("error completion state incorrect")
			}
		}
	})
}
