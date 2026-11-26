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

	"github.com/ysh0566/ponder-go/internal/devstatus"
	"github.com/ysh0566/ponder-go/logging"
	"github.com/ysh0566/ponder-go/schema"
)

type nativeProject struct {
	root, pkg, mode, namespace string
	env                        []string
	out, diagnostics           io.Writer
	log                        *logging.Logger
	dashboard                  *devDashboard
	shutdownTimeout            time.Duration
}

func launchProject(ctx context.Context, mode string, args []string, out, diagnostics io.Writer, log *logging.Logger) error {
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	flags.SetOutput(log.Diagnostics())
	root := flags.String("root", ".", "Go project root")
	pkg := flags.String("app", ".", "Go package to build")
	index := flags.String("schema", os.Getenv("DATABASE_SCHEMA"), "Indexing schema")
	views := flags.String("views-schema", os.Getenv("DATABASE_VIEWS_SCHEMA"), "Published views schema")
	host := flags.String("hostname", os.Getenv("PONDER_HOSTNAME"), "HTTP listener hostname")
	port := flags.String("port", os.Getenv("PONDER_PORT"), "HTTP listener port (0 selects an available port)")
	shutdownTimeout := 5 * time.Second
	if mode == "start" || mode == "dev" {
		flags.DurationVar(&shutdownTimeout, "shutdown-timeout", shutdownTimeout, "Grace period for the indexer to finish after a stop signal")
	}
	var disableUI bool
	if mode == "dev" {
		flags.BoolVar(&disableUI, "disable-ui", false, "Disable the terminal progress display")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if shutdownTimeout <= 0 {
		return fmt.Errorf("shutdown timeout must be positive")
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
	project := nativeProject{shutdownTimeout: shutdownTimeout, root: dir, pkg: *pkg, mode: mode, namespace: *index, out: out, diagnostics: diagnostics, log: log, env: append(os.Environ(), "PONDER_COMMAND="+mode, "PONDER_LOG_LEVEL="+log.Settings().Level, "PONDER_LOG_FORMAT="+log.Settings().Format)}
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
		if cfg := log.Settings(); !disableUI && cfg.Format == "pretty" && cfg.Level != "silent" {
			if ui := newDevDashboard(out, diagnostics, filepath.Join(buildDir, "progress.json")); ui != nil {
				project.dashboard = ui
				defer ui.close()
				project.out = devOutput{ui, out}
				project.diagnostics = devOutput{ui, diagnostics}
				project.log, err = logging.New(project.diagnostics, cfg)
				if err != nil {
					return err
				}
			}
		}
		return project.watch(ctx, buildDir)
	}
	binary := filepath.Join(buildDir, "indexer")
	if err = project.build(ctx, binary); err != nil {
		return err
	}
	command := project.command(ctx, binary, *index)
	log.Debug(map[string]any{"msg": "Starting indexer process", "command": mode, "schema": *index})
	return waitProject(ctx, command.Run(), command)
}

func (p *nativeProject) build(ctx context.Context, binary string) error {
	p.log.Debug(map[string]any{"msg": "Building Go project", "package": p.pkg})
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, p.pkg)
	build.Dir = p.root
	build.Stdout = p.out
	build.Stderr = p.log.Diagnostics()
	return build.Run()
}

func (p *nativeProject) command(ctx context.Context, binary, namespace string) *exec.Cmd {
	command := exec.CommandContext(ctx, binary)
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = p.shutdownTimeout
	command.Dir = p.root
	command.Env = append([]string(nil), p.env...)
	if namespace != "" {
		command.Env = append(command.Env, "PONDER_SHADOW_SCHEMA="+namespace)
	}
	if p.mode == "dev" {
		path := ""
		if p.dashboard != nil {
			path = p.dashboard.file
		}
		command.Env = append(command.Env, "DATABASE_SCHEMA="+namespace, devstatus.FileEnv+"="+path)
	}
	command.Stdin = os.Stdin
	command.Stdout = p.out
	command.Stderr = p.diagnostics
	return command
}

func waitProject(ctx context.Context, err error, command *exec.Cmd) error {
	if errors.Is(err, context.Canceled) && ctx.Err() != nil && command.ProcessState != nil && command.ProcessState.Success() {
		return nil
	}
	return err
}
