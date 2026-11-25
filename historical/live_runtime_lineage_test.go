package historical

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/store"
)

func lineageTestHead(chain, number uint64) store.ManagedLiveHead {
	return store.ManagedLiveHead{
		Checkpoint: checkpoint.MustEncode(checkpoint.Block(1000+number/2, chain, number, true)),
		Hash:       fmt.Sprintf("0x%064x", number+1),
		ParentHash: fmt.Sprintf("0x%064x", number),
	}
}

func TestRuntimeFinalizedLineage(t *testing.T) {
	for _, size := range []uint64{1, memoryLineageHeads, memoryLineageHeads + 1, 13000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			const chain, low = 8453, 5
			high := low + size - 1
			l, err := newRuntimeLineage(chain, high, low)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := l.close(); err != nil {
					t.Error(err)
				}
				files, err := filepath.Glob(filepath.Join(dir, "ponder-finalized-headers-*"))
				if err != nil || len(files) != 0 {
					t.Errorf("remaining spool files %v (%v)", files, err)
				}
			})
			if (l.file != nil) != (size > memoryLineageHeads) {
				t.Fatal("unexpected storage for range")
			}
			for n := high; n >= low; n-- {
				if err := l.append(n, lineageTestHead(chain, n)); err != nil {
					t.Fatal(err)
				}
			}
			if len(l.heads) > memoryLineageHeads {
				t.Fatalf("retained %d in memory", len(l.heads))
			}
			if _, err := l.head(low); err == nil {
				t.Fatal("read unsealed proof")
			}
			if err := l.finish(); err != nil {
				t.Fatal(err)
			}
			for _, n := range []uint64{low, high, low + (high-low)/2, low, high} {
				got, err := l.head(n)
				if err != nil || got != lineageTestHead(chain, n) {
					t.Fatalf("block %d: %+v (%v)", n, got, err)
				}
			}
			for _, n := range []uint64{low - 1, high + 1} {
				if _, err := l.head(n); err == nil {
					t.Fatalf("read outside range at %d", n)
				}
			}
			if l.file != nil {
				info, err := l.file.Stat()
				if err != nil {
					t.Fatal(err)
				}
				if info.Size() != int64(size)*lineageRecordSize || info.Mode().Perm()&0077 != 0 {
					t.Fatalf("spool size %d mode %s", info.Size(), info.Mode())
				}
				if err := l.file.Truncate(info.Size() - 1); err != nil {
					t.Fatal(err)
				}
				if _, err := l.head(low); err == nil {
					t.Fatal("read truncated header")
				}
			}
		})
	}
}

func TestRuntimeFinalizedLineageIncomplete(t *testing.T) {
	for _, size := range []uint64{2, memoryLineageHeads + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			l, err := newRuntimeLineage(1, size, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer l.close()
			bad := lineageTestHead(1, size)
			bad.ParentHash = "0x1234"
			if err := l.append(size, bad); err == nil {
				t.Fatal("accepted invalid hash")
			}
			if err := l.append(size, lineageTestHead(2, size)); err == nil {
				t.Fatal("accepted another chain")
			}
			if err := l.append(size-1, lineageTestHead(1, size-1)); err == nil {
				t.Fatal("accepted gap")
			}
			if err := l.append(size, lineageTestHead(1, size)); err != nil {
				t.Fatal(err)
			}
			if err := l.append(size, lineageTestHead(1, size)); err == nil {
				t.Fatal("accepted duplicate")
			}
			if err := l.finish(); err == nil {
				t.Fatal("sealed incomplete range")
			}
		})
	}
}

func TestRuntimeFinalizedLineageCancellation(t *testing.T) {
	head := lineageTestHead(1, 2)
	r := RuntimeRunner{finalized: map[uint64]store.ManagedLiveHead{1: head}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.walkFinalizedLineage(ctx, nil, 1, head, func(uint64, store.ManagedLiveHead) error {
		t.Fatal("visited header after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition: %v", err)
	}
	sentinel := errors.New("spool unavailable")
	err = r.walkFinalizedLineage(context.Background(), nil, 1, head, func(uint64, store.ManagedLiveHead) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("write failure: %v", err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if _, err := newRuntimeLineage(1, 13000, 1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool creation: %v", err)
	}
}

func TestRuntimeFinalizedHeaderHashes(t *testing.T) {
	for _, key := range []string{"hash", "parentHash"} {
		for _, value := range []string{"0x01", "0x" + strings.Repeat("z", 64)} {
			b := finiteBlock(1, 5)
			b[key] = value
			if _, err := runtimeHead(1, b); err == nil {
				t.Fatalf("accepted malformed %s: %q", key, value)
			}
		}
	}
}
