package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const pgliteDirectoryMarker = ".ponder-go-pglite"

func OpenPGlite(ctx context.Context, directory string) (Backend, error) {
	if directory == "" || (strings.Contains(directory, "://") && !strings.HasPrefix(directory, "file://")) {
		return openPGlite(ctx, directory, nil)
	}
	return openPGliteDirectory(ctx, directory, false)
}

// OpenExistingPGliteDirectory opens a stopped native directory for administration.
// A missing directory or an unrelated PostgreSQL cluster must not be initialized.
func OpenExistingPGliteDirectory(ctx context.Context, directory string) (Backend, error) {
	return openPGliteDirectory(ctx, directory, true)
}

func openPGliteDirectory(ctx context.Context, directory string, existing bool) (Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := canonicalIndependentDirectory(strings.TrimPrefix(directory, "file://"))
	if err != nil {
		return nil, err
	}
	if path == "memory://" {
		return nil, fmt.Errorf("a persistent PGlite directory is required")
	}
	release, files, err := reservePGliteFilesystem(!existing, path)
	if err != nil {
		return nil, err
	}
	marker := filepath.Join(path, pgliteDirectoryMarker)
	if existing {
		for _, name := range []string{marker, filepath.Join(path, "PG_VERSION"), filepath.Join(path, "global", "pg_control")} {
			if stat, err := os.Lstat(name); err != nil || !stat.Mode().IsRegular() {
				release()
				return nil, fmt.Errorf("not an initialized Ponder PGlite directory: %q", path)
			}
		}
	}
	backend, err := openPGlite(ctx, path, files)
	if err != nil {
		release()
		return nil, err
	}
	// The engine inherits these open file descriptions. Closing our copies
	// without LOCK_UN fences the directory until the engine itself exits,
	// including when the application is killed without running deferred cleanup.
	for _, f := range files {
		_ = f.Close()
	}
	if !existing {
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		switch {
		case err == nil:
			err = f.Close()
		case errors.Is(err, os.ErrExist):
			info, statErr := os.Lstat(marker)
			err = statErr
			if err == nil && !info.Mode().IsRegular() {
				err = fmt.Errorf("invalid PGlite directory marker %q", marker)
			}
		}
		if err != nil {
			_ = backend.Close()
			return nil, err
		}
	}
	return backend, nil
}
