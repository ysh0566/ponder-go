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
	"runtime/debug"

	"github.com/ysh0566/ponder-go/examples"
	"strings"
)

//go:embed all:scaffold/empty
var scaffolds embed.FS

var projectName = regexp.MustCompile(`^[a-z0-9-][a-z0-9._-]*$`)

func createProject(ctx context.Context, template, dir string, out io.Writer) error {
	var files fs.FS
	var prefix string
	switch template {
	case "go":
		files, prefix = examples.ERC20, "erc20"
	case "typescript":
		files, prefix = scaffolds, "scaffold/empty"
	default:
		return fmt.Errorf("unknown project template %q", template)
	}
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
	err = fs.WalkDir(files, prefix, func(src string, entry fs.DirEntry, err error) error {
		if err != nil || src == prefix {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative := strings.TrimPrefix(src, prefix+"/")
		if key, ok := strings.CutPrefix(entry.Name(), "_dot_"); ok {
			relative = path.Join(path.Dir(relative), "."+key)
		}
		if relative == "go.mod.tmpl" {
			relative = "go.mod"
		}
		to := filepath.Join(target, filepath.FromSlash(relative))
		if entry.IsDir() {
			if err := os.Mkdir(to, 0755); err != nil {
				return err
			}
			created = append(created, to)
			return nil
		}
		data, err := fs.ReadFile(files, src)
		if err != nil {
			return err
		}
		if relative == "package.json" || relative == "go.mod" {
			data = bytes.Replace(data, []byte("__PROJECT_NAME__"), []byte(name), 1)
		}
		if relative == "go.mod" {
			if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == "github.com/ysh0566/ponder-go" && strings.HasPrefix(info.Main.Version, "v") {
				version := strings.TrimSuffix(info.Main.Version, "+dirty")
				data = append(data, []byte("\nrequire github.com/ysh0566/ponder-go "+version+"\n")...)
			}
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
