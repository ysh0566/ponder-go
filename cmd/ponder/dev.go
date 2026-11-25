package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ysh0566/ponder-go/schema"
)

// projectRevision includes Go sources, module metadata and the config/schema
// files read by the native scaffold. A polling scan also detects atomic saves.
func (p *nativeProject) projectRevision() (string, error) {
	files := sha256.New()
	fmt.Fprintf(files, "%q %q %q\n", p.root, p.pkg, p.namespace)
	err := filepath.WalkDir(p.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != p.root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(name) {
		case ".go", ".json", ".sql", ".graphql", ".gql", ".s", ".c", ".h":
		default:
			if name != "go.mod" && name != "go.sum" && name != "go.work" && name != "go.work.sum" {
				return nil
			}
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		rel, err := filepath.Rel(p.root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(files, "%q %x\n", filepath.ToSlash(rel), hash.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", files.Sum(nil)), nil
}

func (p *nativeProject) devNamespace(revision string) string {
	base := p.namespace
	if base == "" {
		base = "ponder_go_dev"
	} else if !strings.HasPrefix(base, "ponder_go_") {
		base = "ponder_go_" + base
	}
	return base[:min(len(base), 24)] + "_" + revision[:20]
}

func (p *nativeProject) watch(ctx context.Context, dir string) error {
	if p.namespace != "" && !schema.Identifier(p.namespace) {
		return fmt.Errorf("invalid dev schema prefix %q", p.namespace)
	}
	timer := time.NewTicker(300 * time.Millisecond)
	defer timer.Stop()
	var last, runningRevision, binary string
	var stopped chan error
	var stop context.CancelFunc
	stopChild := func() {
		if stop != nil {
			stop()
			stop = nil
		}
		if stopped != nil {
			<-stopped
			stopped = nil
		}
		if binary != "" {
			os.Remove(binary)
			binary = ""
		}
	}
	defer stopChild()
	for ctx.Err() == nil {
		revision, err := p.projectRevision()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		if err == nil && stopped != nil && revision == runningRevision {
			last = revision
		}
		if err == nil && revision != last {
			last = revision
			next := filepath.Join(dir, "indexer-a")
			if next == binary {
				next = filepath.Join(dir, "indexer-b")
			}
			if err = p.build(ctx, next); err != nil {
				if ctx.Err() == nil {
					fmt.Fprintln(os.Stderr, "Go build failed, waiting for an edit:", err)
				}
			} else {
				after, err := p.projectRevision()
				if err != nil || after != revision {
					os.Remove(next)
					last = ""
					continue
				}
				stopChild()
				if ctx.Err() != nil {
					os.Remove(next)
					break
				}
				run, cancel := context.WithCancel(ctx)
				namespace := p.devNamespace(revision)
				child := p.command(run, next, namespace)
				if err = child.Start(); err != nil {
					cancel()
					os.Remove(next)
					return err
				}
				stop = cancel
				binary = next
				runningRevision = revision
				stopped = make(chan error, 1)
				go func(done chan<- error) { done <- child.Wait() }(stopped)
				fmt.Fprintf(os.Stderr, "Watching %s, dev schema %s\n", p.root, namespace)
			}
		}
		select {
		case <-ctx.Done():
		case <-timer.C:
		case err := <-stopped:
			stopped = nil
			if err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "Indexer exited, waiting for an edit:", err)
			}
		}
	}
	return nil
}
