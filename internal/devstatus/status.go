// Package devstatus exchanges progress between a watched application and its CLI.
package devstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FileEnv is supplied only to a child whose watcher renders progress.
const FileEnv = "PONDER_DEV_STATUS_FILE"

type Snapshot struct {
	Address              string
	Ready                bool
	ReadinessUnavailable bool
	// Nil means progress could not be read. An empty map means no checkpoint yet.
	Blocks map[string]uint64
	Chains map[string]ChainMetrics `json:",omitempty"`
	Events map[string]EventMetrics `json:",omitempty"`
}

type ChainMetrics struct {
	RequestsPerSecond float64
	Backfill          *Backfill `json:",omitempty"`
}

type Backfill struct {
	Completed, Total uint64
	ETASeconds       *float64 `json:",omitempty"`
}

type EventMetrics struct {
	Count         uint64
	AverageMillis float64
}

func (s Snapshot) Write(path string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".progress-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(s); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
