package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ysh0566/ponder-go/schema"
)

type nativeProject struct {
	root, pkg, mode, namespace string
	env                        []string
	out                        io.Writer
}

func launchProject(ctx context.Context, mode string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	root := flags.String("root", ".", "Go project root")
	pkg := flags.String("app", ".", "Go package to build")
	index := flags.String("schema", os.Getenv("DATABASE_SCHEMA"), "Indexing schema")
	views := flags.String("views-schema", os.Getenv("DATABASE_VIEWS_SCHEMA"), "Published views schema")
	host := flags.String("hostname", os.Getenv("PONDER_HOSTNAME"), "HTTP listener hostname")
	port := flags.String("port", os.Getenv("PONDER_PORT"), "HTTP listener port (0 selects an available port)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if err := schema.ValidateNamespaces(*index, *views); err != nil {
		return err
	}
	if *port != "" {
		n, err := strconv.Atoi(*port)
		if err != nil || n < 0 || n > 65535 {
			return fmt.Errorf("invalid TCP port %q", *port)
		}
	}
	dir, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	project := nativeProject{root: dir, pkg: *pkg, mode: mode, namespace: *index, out: out, env: append(os.Environ(), "PONDER_COMMAND="+mode)}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "schema":
			project.env = append(project.env, "DATABASE_SCHEMA="+*index)
		case "views-schema":
			project.env = append(project.env, "DATABASE_VIEWS_SCHEMA="+*views)
		case "hostname":
			project.env = append(project.env, "PONDER_HOSTNAME="+*host)
		case "port":
			project.env = append(project.env, "PONDER_PORT="+*port)
		}
	})
	base := filepath.Join(dir, ".ponder-go", "bin")
	if err = os.MkdirAll(base, 0755); err != nil {
		return err
	}
	buildDir, err := os.MkdirTemp(base, mode+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildDir)
	if mode == "dev" {
		return project.watch(ctx, buildDir)
	}
	binary := filepath.Join(buildDir, "indexer")
	if err = project.build(ctx, binary); err != nil {
		return err
	}
	command := project.command(ctx, binary, *index)
	return waitProject(ctx, command.Run(), command)
}

func (p *nativeProject) build(ctx context.Context, binary string) error {
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, p.pkg)
	build.Dir = p.root
	build.Stdout = p.out
	build.Stderr = os.Stderr
	return build.Run()
}

func (p *nativeProject) command(ctx context.Context, binary, namespace string) *exec.Cmd {
	command := exec.CommandContext(ctx, binary)
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 5 * time.Second
	command.Dir = p.root
	command.Env = append([]string(nil), p.env...)
	if namespace != "" {
		command.Env = append(command.Env, "PONDER_SHADOW_SCHEMA="+namespace)
	}
	if p.mode == "dev" {
		command.Env = append(command.Env, "DATABASE_SCHEMA="+namespace)
	}
	command.Stdin = os.Stdin
	command.Stdout = p.out
	command.Stderr = os.Stderr
	return command
}

func waitProject(ctx context.Context, err error, command *exec.Cmd) error {
	if errors.Is(err, context.Canceled) && ctx.Err() != nil && command.ProcessState != nil && command.ProcessState.Success() {
		return nil
	}
	return err
}
