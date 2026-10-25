package indexing

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"ponder.local/ponder-go/store"
)

type adapterErrorState struct{ err error }

func (s *adapterErrorState) GetRetryableError() error  { return s.err }
func (s *adapterErrorState) SetRetryableError(e error) { s.err = e }
func (s *adapterErrorState) ClearRetryableError()      { s.err = nil }
func TestDatabaseErrorSourceClassification(t *testing.T) {
	for _, message := range []string{"terminating connection due to administrator command", "connection to client lost", "too many clients already", "Connection terminated unexpectedly", "ECONNRESET", "ETIMEDOUT", "timeout exceeded when trying to connect"} {
		original := errors.New(message)
		parsed := ParseDatabaseError(original)
		var typed *DatabaseError
		if !errors.As(parsed, &typed) || typed.ErrorName() != "DbConnectionError" || !typed.Retryable() || !errors.Is(parsed, original) {
			t.Fatalf("bad classification %v", parsed)
		}
	}
	for _, test := range []struct{ message, name string }{{"violates not-null constraint", "NotNullConstraintError"}, {"violates unique constraint", "UniqueConstraintError"}, {"violates check constraint", "CheckConstraintError"}, {"Do not know how to serialize a BigInt", "BigIntSerializationError"}, {"cannot serialize BigInt", "BigIntSerializationError"}, {"table does not exist", "NonRetryableUserError"}, {"table already exists", "NonRetryableUserError"}, {"violates unique constraint ECONNRESET", "UniqueConstraintError"}} {
		parsed := ParseDatabaseError(errors.New(test.message))
		e, ok := parsed.(*DatabaseError)
		if !ok || e.Name != test.name || e.Retryable() {
			t.Fatalf("bad classification %v", parsed)
		}
	}
	for _, e := range []error{errors.New("connection refused"), errors.New("generic backend failure"), &store.UniqueConstraintError{}, &InvalidEventAccessError{Key: "gas"}, context.Canceled, fmt.Errorf("ECONNRESET: %w", context.DeadlineExceeded)} {
		if ParseDatabaseError(e) != e {
			t.Fatalf("identity changed %T", e)
		}
	}
}
func TestDatabaseStickyOnlyRetryable(t *testing.T) {
	state := &adapterErrorState{}
	underlying := &store.IndexingStore{}
	db := NewDatabase(underlying, state)
	if db.Underlying() != underlying {
		t.Fatal("underlying identity lost")
	}
	ordinary := errors.New("ordinary failure")
	if db.capture(ordinary) != ordinary || state.err != nil {
		t.Fatal("ordinary failure became sticky")
	}
	retryable := db.capture(errors.New("ECONNRESET"))
	if state.err != retryable {
		t.Fatal("retryable not sticky")
	}
	if db.capture(nil) != nil || state.err != retryable {
		t.Fatal("success cleared caught retryable")
	}
	db.capture(ordinary)
	if state.err != retryable {
		t.Fatal("ordinary error replaced retryable")
	}
	state.ClearRetryableError()
	if state.err != nil {
		t.Fatal("clear failed")
	}
}
