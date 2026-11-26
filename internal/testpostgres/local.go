package testpostgres

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type localCluster struct {
	bin, data, log string
}

// startLocal creates a private cluster from installed server binaries. Only its
// Unix socket accepts connections. pg_ctl's immediate stop forces crash recovery.
func (s *Server) startLocal(t *testing.T, bin string) {
	t.Helper()
	// Keep socket paths short even in deeply nested subtests.
	root, err := os.MkdirTemp("", "ponder-pg-")
	if err != nil {
		t.Fatal(err)
	}
	cluster := &localCluster{bin: bin, data: filepath.Join(root, "data"), log: filepath.Join(root, "postgres.log")}
	t.Cleanup(func() {
		if _, err := os.Stat(filepath.Join(cluster.data, "postmaster.pid")); !os.IsNotExist(err) {
			if err := cluster.run("stop", "-m", "fast"); err != nil {
				t.Error(err)
				return
			}
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	passfile := filepath.Join(root, "init.pw")
	password, _ := s.endpoint.User.Password()
	if err := os.WriteFile(passfile, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	init := exec.CommandContext(ctx, filepath.Join(bin, "initdb"), "-D", cluster.data,
		"--no-locale", "--encoding=UTF8", "--auth=scram-sha-256", "--username=postgres", "--pwfile="+passfile)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("initialize disposable PostgreSQL: %v\n%s", err, out)
	}
	if err := os.Remove(passfile); err != nil {
		t.Fatal(err)
	}
	// PostgreSQL changes its working directory to PGDATA on startup.
	const settings = "listen_addresses = ''\nunix_socket_directories = '.'\nport = 5432\nshared_buffers = '16MB'\n"
	if err := os.WriteFile(filepath.Join(cluster.data, "postgresql.auto.conf"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	query := s.endpoint.Query()
	query.Set("host", cluster.data)
	s.endpoint.RawQuery = query.Encode()
	s.local = cluster
	cluster.control(t, "start")
}

func (c *localCluster) run(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	flags := append([]string{"-D", c.data, "-l", c.log, "-w", "-t", "45"}, args...)
	if out, err := exec.CommandContext(ctx, filepath.Join(c.bin, "pg_ctl"), flags...).CombinedOutput(); err != nil {
		return fmt.Errorf("pg_ctl %v: %w\n%s", args, err, out)
	}
	return nil
}

func (c *localCluster) control(t *testing.T, args ...string) {
	t.Helper()
	if err := c.run(args...); err != nil {
		t.Fatal(err)
	}
}
