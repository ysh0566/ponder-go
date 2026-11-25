package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed all:scaffold/empty
var scaffolds embed.FS

var projectName = regexp.MustCompile(`^[a-z0-9-][a-z0-9._-]*$`)

func createEmptyProject(ctx context.Context, dir string, out io.Writer) error {
	target, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return err
	}
	name := filepath.Base(target)
	if !projectName.MatchString(name) || len(name) > 214 || name == "node_modules" || name == "favicon.ico" {
		return fmt.Errorf("invalid project name %q", name)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return err
	}
	created := []string{target}
	err = fs.WalkDir(scaffolds, "scaffold/empty", func(src string, entry fs.DirEntry, err error) error {
		if err != nil || src == "scaffold/empty" {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative := strings.TrimPrefix(src, "scaffold/empty/")
		if key, ok := strings.CutPrefix(entry.Name(), "_dot_"); ok {
			relative = path.Join(path.Dir(relative), "."+key)
		}
		to := filepath.Join(target, filepath.FromSlash(relative))
		if entry.IsDir() {
			if err := os.Mkdir(to, 0755); err != nil {
				return err
			}
			created = append(created, to)
			return nil
		}
		data, err := scaffolds.ReadFile(src)
		if err != nil {
			return err
		}
		if relative == "package.json" {
			data = bytes.Replace(data, []byte("__PROJECT_NAME__"), []byte(name), 1)
		}
		f, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		created = append(created, to)
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	})
	if err != nil {
		// Only remove entries this invocation created. Concurrent unrelated
		// files keep their parent directories.
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.Remove(created[i])
		}
		return err
	}
	_, err = fmt.Fprintln(out, target)
	return err
}
