package store

import (
	"context"
	"time"

	"github.com/ysh0566/ponder-go/metrics"
)

// The method is a fixed operation name. SQL and arguments never become labels.
func databaseQuery(ctx context.Context, db Querier, method, statement string, args ...any) (Result, error) {
	start := time.Now()
	result, err := db.Query(ctx, statement, args...)
	metrics.ObserveDatabaseMethod(ctx, method, time.Since(start), err)
	return result, err
}

func databaseExec(ctx context.Context, db Querier, method, statement string, args ...any) error {
	start := time.Now()
	err := db.Exec(ctx, statement, args...)
	metrics.ObserveDatabaseMethod(ctx, method, time.Since(start), err)
	return err
}
