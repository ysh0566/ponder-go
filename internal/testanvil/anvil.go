// Package testanvil starts an isolated local chain for integration tests.
package testanvil

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Start returns the HTTP URL of a fresh chain with the upstream fixture's chain ID.
func Start(t *testing.T, options ...string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, "anvil", append([]string{"--host", "127.0.0.1", "--port", "0", "--chain-id", "1"}, options...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			if addr, ok := strings.CutPrefix(s.Text(), "Listening on 127.0.0.1:"); ok {
				ready <- "http://127.0.0.1:" + addr
			}
		}
		done <- cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case url := <-ready:
		return url
	case err := <-done:
		t.Fatalf("Anvil exited before binding its port: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("Anvil timed out while binding its port")
	}
	return ""
}
