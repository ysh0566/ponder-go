package indexing

// Supplemental deadline identity contract only, not real SQL or a timed-expiry
// test. Cancellation is explicit and observed through Done, never inferred from
// elapsed time. Actual PostgreSQL lifecycle tests are maintained separately.
import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHistoricalPersistencePermitDeadlines(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		name := "parent_deadline_both_contexts"
		if earlier {
			name = "earlier_schedule_deadline_is_soft"
		}
		t.Run(name, func(t *testing.T) {
			parentDeadline := time.Now().Add(time.Hour)
			parent, parentCancel := context.WithDeadline(context.Background(), parentDeadline)
			defer parentCancel()
			scope, root := draftPersistenceScope(parent)
			defer scope.cancel()
			schedule := root
			softStop := func() {}
			wantWorkDeadline := parentDeadline
			if earlier {
				wantWorkDeadline = parentDeadline.Add(-30 * time.Minute)
				schedule, softStop = context.WithDeadline(root, wantWorkDeadline)
			}
			defer softStop()
			checkDeadline := func(label string, ctx context.Context, want time.Time) {
				t.Helper()
				got, ok := ctx.Deadline()
				if !ok || !got.Equal(want) {
					t.Fatalf("%s deadline: got=%v present=%v want=%v", label, got, ok, want)
				}
			}
			waitDone := func(label string, ctx context.Context) {
				t.Helper()
				timer := time.NewTimer(5 * time.Second)
				defer timer.Stop()
				select {
				case <-ctx.Done():
				case <-timer.C:
					t.Fatalf("%s did not receive explicit parent cancellation", label)
				}
			}
			err := scope.WithHistoricalPersistenceContext(schedule, scope.PersistenceContext(), func(ioCtx, workCtx context.Context) error {
				checkDeadline("io before stop", ioCtx, parentDeadline)
				checkDeadline("work before stop", workCtx, wantWorkDeadline)
				if ioCtx.Err() != nil || workCtx.Err() != nil {
					t.Fatal("future deadlines were unexpectedly cancelled", ioCtx.Err(), workCtx.Err())
				}
				if earlier {
					softStop()
					waitDone("work soft stop", workCtx)
					if !errors.Is(workCtx.Err(), context.Canceled) || ioCtx.Err() != nil || parent.Err() != nil {
						t.Fatal("soft schedule stop affected parent I/O", workCtx.Err(), ioCtx.Err(), parent.Err())
					}
					select {
					case <-ioCtx.Done():
						t.Fatal("soft stop closed canonical I/O Done")
					default:
					}
					checkDeadline("io after soft stop", ioCtx, parentDeadline)
					checkDeadline("work after soft stop", workCtx, wantWorkDeadline)
				}
				parentCancel()
				waitDone("io hard stop", ioCtx)
				waitDone("work hard stop", workCtx)
				if !errors.Is(ioCtx.Err(), context.Canceled) || !errors.Is(workCtx.Err(), context.Canceled) {
					t.Fatal("explicit parent cancellation lost", ioCtx.Err(), workCtx.Err())
				}
				checkDeadline("io after parent cancel", ioCtx, parentDeadline)
				checkDeadline("work after parent cancel", workCtx, wantWorkDeadline)
				return ioCtx.Err()
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("parent cancellation error not retained: %v", err)
			}
			waitOwnedGate(scope.gate)
		})
	}
}
