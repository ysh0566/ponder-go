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
	"strings"

	"github.com/ysh0566/ponder-go/examples"
)

//go:embed all:scaffold
var scaffolds embed.FS

var goProjectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var projectName = regexp.MustCompile(`^[a-z0-9-][a-z0-9._-]*$`)

func createProject(ctx context.Context, template, dir string, out io.Writer) error {
	var files fs.FS
	var prefix string
	var copyGoBootstrap bool
	switch template {
	case "go", "erc20":
		files, prefix = examples.ERC20, "erc20"
	case "empty":
		files, prefix = scaffolds, "scaffold/go-empty"
		copyGoBootstrap = true
	case "multichain", "filter", "friendtech", "uniswap-v3-flash", "reference-erc20",
		"blocks", "api-functions", "call-traces", "read-contract", "erc721", "erc1155", "erc4626", "factory", "accounts", "proxy":
		files, prefix = scaffolds, "scaffold/"+template
		copyGoBootstrap = true
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
	valid := goProjectName.MatchString(name)
	if template == "typescript" {
		valid = projectName.MatchString(name) && len(name) <= 214 && name != "node_modules" && name != "favicon.ico"
	}
	if !valid {
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
	copyFile := func(source fs.FS, file, relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := fs.ReadFile(source, file)
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
		to := filepath.Join(target, filepath.FromSlash(relative))
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
	}
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
		relative = strings.TrimSuffix(relative, ".tmpl")
		to := filepath.Join(target, filepath.FromSlash(relative))
		if entry.IsDir() {
			if err := os.Mkdir(to, 0755); err != nil {
				return err
			}
			created = append(created, to)
			return nil
		}
		return copyFile(files, src, relative)
	})
	if err == nil && template == "reference-erc20" {
		// Both mainnet ERC-20 examples index the same contract, events and tables.
		for _, name := range []string{"abi.json", "network.json", "schema.json"} {
			if err = copyFile(scaffolds, "scaffold/api-functions/"+name, name); err != nil {
				break
			}
		}
	}
	if err == nil && copyGoBootstrap {
		for _, name := range []string{"main.go", "indexer.go", "network.go", "api.go", "go.mod.tmpl", "package.json", "_dot_gitignore"} {
			if _, err := fs.Stat(files, path.Join(prefix, name+".tmpl")); err == nil {
				continue // This project supplies its own implementation.
			}
			relative := strings.TrimSuffix(name, ".tmpl")
			if name == "_dot_gitignore" {
				relative = ".gitignore"
			}
			source, from := fs.FS(examples.ERC20), "erc20/"+name
			if name == "api.go" {
				source, from = scaffolds, "scaffold/graphql.go.tmpl"
			}
			if err = copyFile(source, from, relative); err != nil {
				break
			}
		}
	}
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
