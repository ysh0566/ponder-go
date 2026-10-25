package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// A fixed per-user registry coordinates parent/child paths across processes.
// Record files are deliberately retained; an unlocked record is not ownership.
// Directory-inode flocks also fence aliases that identify the same inode,
// without putting a file in a new PGlite data directory (initdb requires empty).
func reserveIndependentFilesystem(a, b string) (func(), []*os.File, error) {
	if a == "memory://" && b == "memory://" {
		return func() {}, nil, nil
	}
	registry := filepath.Join("/tmp", fmt.Sprintf("ponder-go-independent-owners-%d", os.Getuid()))
	if err := os.MkdirAll(registry, 0700); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(registry)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("unsafe independent ownership registry")
	}
	guard, err := openIndependentLock(filepath.Join(registry, "coordination"))
	if err != nil {
		return nil, nil, err
	}
	defer guard.Close()
	if err = syscall.Flock(int(guard.Fd()), syscall.LOCK_EX); err != nil {
		return nil, nil, err
	}
	defer syscall.Flock(int(guard.Fd()), syscall.LOCK_UN)
	var held []*os.File
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = syscall.Flock(int(held[i].Fd()), syscall.LOCK_UN)
			_ = held[i].Close()
		}
	}
	fail := func(e error) (func(), []*os.File, error) { release(); return nil, nil, e }
	entries, err := os.ReadDir(registry)
	if err != nil {
		return fail(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "owner-") {
			continue
		}
		file, e := openIndependentLock(filepath.Join(registry, entry.Name()))
		if e != nil {
			return fail(e)
		}
		e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			file.Close()
			continue
		}
		if !errors.Is(e, syscall.EWOULDBLOCK) {
			file.Close()
			return fail(e)
		}
		data, e := io.ReadAll(io.LimitReader(file, 8192))
		file.Close()
		if e != nil {
			return fail(e)
		}
		path := string(data)
		if independentDirectoriesOverlap(a, path) || independentDirectoriesOverlap(b, path) {
			return fail(fmt.Errorf("PGlite directory overlaps an active filesystem owner"))
		}
	}
	for _, path := range []string{a, b} {
		if path == "memory://" {
			continue
		}
		sum := sha256.Sum256([]byte(path))
		record, e := openIndependentLock(filepath.Join(registry, fmt.Sprintf("owner-%x", sum)))
		if e != nil {
			return fail(e)
		}
		if e = syscall.Flock(int(record.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			record.Close()
			return fail(fmt.Errorf("PGlite directory already owned: %w", e))
		}
		held = append(held, record)
		old, e := io.ReadAll(record)
		if e != nil {
			return fail(e)
		}
		if len(old) > 0 && string(old) != path {
			return fail(fmt.Errorf("independent ownership record identity mismatch"))
		}
		if len(old) == 0 {
			if _, e = record.WriteAt([]byte(path), 0); e != nil {
				return fail(e)
			}
		}
		if e = os.MkdirAll(path, 0700); e != nil {
			return fail(e)
		}
		local, e := os.Open(path)
		if e != nil {
			return fail(e)
		}
		if e = syscall.Flock(int(local.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			local.Close()
			return fail(fmt.Errorf("PGlite directory lock already held: %w", e))
		}
		held = append(held, local)
	}
	var once sync.Once
	return func() { once.Do(release) }, held, nil
}
func openIndependentLock(path string) (*os.File, error) {
	// Do not follow attacker/user supplied symlinks or rewrite existing contents.
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("invalid independent lock file %q", path)
	}
	return file, nil
}
