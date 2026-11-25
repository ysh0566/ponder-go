package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/ysh0566/ponder-go/logging"
)

// Global options may appear before or after the command. Preserve values
// belonging to subcommand flags, positional arguments, and the -- terminator.
func logOptions(args []string) ([]string, logging.Settings, error) {
	cfg := logging.Environment()
	var rest []string
	var explicit string
	debug, trace := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		key, value, hasValue := strings.Cut(arg, "=")
		switch key {
		case "--log-level", "--log-format":
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return nil, cfg, fmt.Errorf("%s needs a value", key)
				}
				i++
				value = args[i]
			}
			if key == "--log-format" {
				if value != "pretty" && value != "json" {
					return nil, cfg, fmt.Errorf("invalid log format %q", value)
				}
				cfg.Format = value
			} else {
				if !logging.ValidLevel(value) {
					return nil, cfg, fmt.Errorf("invalid log level %q", value)
				}
				explicit = value
			}
		case "-vv", "--trace", "-v", "--debug":
			if hasValue {
				return nil, cfg, fmt.Errorf("%s takes no value", key)
			}
			if key == "-vv" || key == "--trace" {
				trace = true
			} else {
				debug = true
			}
		default:
			rest = append(rest, arg)
			if !hasValue && subcommandValue(key) && i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
		}
	}
	switch {
	case explicit != "":
		cfg.Level = explicit
	case trace:
		cfg.Level = "trace"
	case debug:
		cfg.Level = "debug"
	}
	return rest, cfg, nil
}

func subcommandValue(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	switch strings.TrimLeft(arg, "-") {
	case "root", "app", "schema", "views-schema", "hostname", "port", "template", "database-url", "pglite-dir", "out", "package", "abi", "app-config", "bigint-shape", "abi-parameters", "decoder", "merge-abi", "dynamic-abi":
		return true
	}
	return false
}

// A child process and its watcher may write concurrently to a provided stream.
type cliWriter struct {
	mu   *sync.Mutex
	dest io.Writer
}

func (w cliWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dest.Write(p)
}

func runCLI(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	var mu sync.Mutex
	if _, ok := out.(*os.File); !ok {
		out = cliWriter{&mu, out}
	}
	if _, ok := diagnostics.(*os.File); !ok {
		diagnostics = cliWriter{&mu, diagnostics}
	}
	rest, cfg, parseErr := logOptions(args)
	logger, err := logging.New(diagnostics, cfg)
	if err != nil {
		return err
	}
	if parseErr != nil {
		logger.Error(parseErr.Error())
		return parseErr
	}
	err = runCommand(ctx, rest, out, diagnostics, logger)
	if err != nil {
		logger.Error(err.Error())
	}
	return err
}
