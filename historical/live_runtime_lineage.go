package historical

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/store"
)

// Each chain is authenticated from the finalized anchor down to the durable
// cursor. Small ranges stay in memory. Larger ranges use a private temporary
// file so the number of empty heights does not determine heap usage.
const memoryLineageHeads = 256
const lineageRecordSize = 8 + 32 + 32

type runtimeLineage struct {
	chain, high, length, count uint64
	sealed                     bool
	heads                      [][lineageRecordSize]byte
	file                       *os.File
	writer                     *bufio.Writer
	path                       string
}

func newRuntimeLineage(chain, high, low uint64) (*runtimeLineage, error) {
	l := &runtimeLineage{chain: chain, high: high}
	if high >= low {
		l.length = high - low + 1
	}
	if l.length <= memoryLineageHeads {
		return l, nil
	}
	file, err := os.CreateTemp("", "ponder-finalized-headers-*")
	if err != nil {
		return nil, err
	}
	l.file, l.writer = file, bufio.NewWriterSize(file, 32*1024)
	// Unlink on platforms that allow it while the descriptor is open. A
	// process exit then releases the spool even when deferred cleanup cannot run.
	if err := os.Remove(file.Name()); err != nil {
		l.path = file.Name()
	}
	return l, nil
}

func decodeLineageHash(dst []byte, hash string) error {
	if len(hash) != 66 || !strings.HasPrefix(hash, "0x") {
		return fmt.Errorf("expected a 32-byte hexadecimal block hash")
	}
	_, err := hex.Decode(dst, []byte(hash[2:]))
	return err
}

func (l *runtimeLineage) append(number uint64, h store.ManagedLiveHead) error {
	if l.sealed || l.count >= l.length || number > l.high || l.high-number != l.count {
		return fmt.Errorf("non-contiguous finalized lineage at block %d", number)
	}
	cp, err := checkpoint.Decode(h.Checkpoint)
	if err != nil || cp != checkpoint.Block(cp.BlockTimestamp, l.chain, number, true) {
		return fmt.Errorf("invalid finalized lineage checkpoint at %d", number)
	}
	var record [lineageRecordSize]byte
	binary.BigEndian.PutUint64(record[:8], cp.BlockTimestamp)
	for i, hash := range [2]string{h.Hash, h.ParentHash} {
		if err := decodeLineageHash(record[8+i*32:8+(i+1)*32], hash); err != nil {
			return fmt.Errorf("invalid finalized lineage hash at %d: %w", number, err)
		}
	}
	if l.file == nil {
		l.heads = append(l.heads, record)
	} else {
		if _, err := l.writer.Write(record[:]); err != nil {
			return err
		}
	}
	l.count++
	return nil
}

func (l *runtimeLineage) finish() error {
	if l.count != l.length {
		return fmt.Errorf("incomplete finalized lineage: have %d of %d headers", l.count, l.length)
	}
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			return err
		}
	}
	l.sealed = true
	return nil
}

func (l *runtimeLineage) head(number uint64) (store.ManagedLiveHead, error) {
	if l == nil || !l.sealed || number > l.high || l.high-number >= l.count {
		return store.ManagedLiveHead{}, fmt.Errorf("missing finalized lineage at block %d", number)
	}
	index := l.high - number
	var record [lineageRecordSize]byte
	if l.file == nil {
		record = l.heads[index]
	} else if _, err := l.file.ReadAt(record[:], int64(index)*lineageRecordSize); err != nil {
		return store.ManagedLiveHead{}, err
	}
	cp, err := checkpoint.Encode(checkpoint.Block(binary.BigEndian.Uint64(record[:8]), l.chain, number, true))
	if err != nil {
		return store.ManagedLiveHead{}, err
	}
	return store.ManagedLiveHead{Checkpoint: cp, Hash: "0x" + hex.EncodeToString(record[8:40]), ParentHash: "0x" + hex.EncodeToString(record[40:])}, nil
}

func (l *runtimeLineage) close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	if l.path != "" {
		err = errors.Join(err, os.Remove(l.path))
	}
	return err
}
