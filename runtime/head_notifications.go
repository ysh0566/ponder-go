package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ysh0566/ponder-go/rpc"
)

// headNotifications coalesces hints while the worker catches up from its durable
// position. Polling continues to cover missed notifications and unavailable WS.
func headNotifications(ctx context.Context, endpoint string) <-chan struct{} {
	if endpoint == "" {
		return nil
	}
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go func() {
		client := rpc.New(endpoint)
		delay := 100 * time.Millisecond
		for {
			heads, err := client.Subscribe(ctx, "newHeads")
			if err == nil {
				// Cover the gap between the first scan and subscription setup.
				signal()
				for raw := range heads {
					var head struct{ Number, Hash string }
					if json.Unmarshal(raw, &head) == nil && head.Number != "" && head.Hash != "" {
						signal()
					}
				}
				return
			}
			if !rpc.IsTransient(err) || ctx.Err() != nil {
				return
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			delay = min(delay*2, 5*time.Second)
		}
	}()
	return wake
}
