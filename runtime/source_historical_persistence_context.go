package runtime

import (
	"context"
	"fmt"
)

// sourceHistoricalPersistenceProvider admits one root persistence operation and
// owns its actual children until the callback returns. The token is a canonical
// scope capability, not a caller-selected replacement cancellation context.
// The provider must reject missing, foreign, derived and revoked tokens and
// must not let scheduling cancellation cancel SQL that was already admitted.
// Both callback contexts carry the same admitted-child capability: workCtx
// keeps scheduling cancellation for RPC; ioCtx keeps the real caller/owner
// lifetime for SQL. Actual caller cancellation and shutdown remain effective.
type sourceHistoricalPersistenceProvider interface {
	WithHistoricalPersistenceContext(schedule context.Context, token context.Context, fn func(ioCtx context.Context, workCtx context.Context) error) error
}

func sourceWithHistoricalPersistence(schedule context.Context, database any, token context.Context, fn func(ioCtx context.Context, workCtx context.Context) error) error {
	provider, owned := database.(sourceHistoricalPersistenceProvider)
	if !owned {
		if token != nil {
			return fmt.Errorf("historical persistence token requires an owned provider")
		}
		// Preserve the standalone source store's original context and behavior.
		return fn(schedule, schedule)
	}
	if token == nil {
		return fmt.Errorf("owned historical persistence requires its scope token")
	}
	return provider.WithHistoricalPersistenceContext(schedule, token, fn)
}

// Validate even an entirely cached/empty range. This never retains an admitted
// context: each later query or transaction must obtain its own fresh root lease.
func sourceValidateHistoricalPersistence(schedule context.Context, database any, token context.Context) error {
	return sourceWithHistoricalPersistence(schedule, database, token, func(context.Context, context.Context) error { return nil })
}
