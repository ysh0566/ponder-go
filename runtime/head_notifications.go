package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ysh0566/ponder-go/rpc"
)

// headNotifications coalesces hints while the worker catches up from its durable
// position. Polling continues to cover missed notifications and unavailable WS.
func headNotifications(ctx context.Context, endpoints ...string) <-chan struct{} {
	var wake chan struct{}
	for _, endpoint := range endpoints {
		if endpoint == "" {
			continue
		}
		if wake == nil {
			wake = make(chan struct{}, 1)
		}
		go watchHeads(ctx, endpoint, wake)
	}
	return wake
}

func watchHeads(ctx context.Context, endpoint string, wake chan<- struct{}) {
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
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
}
