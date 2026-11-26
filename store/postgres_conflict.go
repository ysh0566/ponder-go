package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsPostgresTransactionConflict recognizes server-reported serialization and
// deadlock failures. A failed commit with an unknown outcome requires reopen,
// even if one of its underlying errors carries a transaction conflict code.
func IsPostgresTransactionConflict(err error) bool {
	switch e := err.(type) {
	case *pgconn.PgError:
		return e != nil && (e.Code == "40001" || e.Code == "40P01")
	case nil, *ManagedStageError, *CommitOutcomeUnknownError:
		return false
	case interface{ Unwrap() []error }:
		causes := e.Unwrap()
		for _, cause := range causes {
			if !IsPostgresTransactionConflict(cause) {
				return false
			}
		}
		return len(causes) != 0
	default:
		return IsPostgresTransactionConflict(errors.Unwrap(err))
	}
}
