// Package testpostgres starts an isolated PostgreSQL server for integration tests.
package testpostgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Server owns one disposable container, including its data volume.
type Server struct {
	id       string
	endpoint url.URL
}

func Start(t *testing.T) *Server {
	t.Helper()
	image := os.Getenv("PONDER_TEST_POSTGRES_ENGINE_IMAGE")
	if image == "" {
		t.Skip("set PONDER_TEST_POSTGRES_ENGINE_IMAGE to a local disposable postgres image")
	}
	password := make([]byte, 24)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	server := &Server{endpoint: url.URL{Scheme: "postgres", User: url.UserPassword("postgres", hex.EncodeToString(password)), Path: "/postgres", RawQuery: "sslmode=disable"}}
	server.id = server.docker(t, "run", "--detach", "--pull=never", "--publish", "127.0.0.1::5432", "--env", "POSTGRES_PASSWORD", "--label", "ponder-go-test=postgres", image)
	if _, err := hex.DecodeString(server.id); len(server.id) != 64 || err != nil {
		t.Fatalf("invalid new container ID %q", server.id)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "rm", "--force", "--volumes", server.id).CombinedOutput(); err != nil {
			t.Errorf("remove test container: %v %s", err, out)
		}
	})
	server.waitReady(t)
	return server
}

func (s *Server) docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	password, _ := s.endpoint.User.Password()
	cmd.Env = append(os.Environ(), "POSTGRES_PASSWORD="+password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v %s", args[0], err, out)
	}
	return strings.TrimSpace(string(out))
}

func (s *Server) waitReady(t *testing.T) {
	t.Helper()
	s.endpoint.Host = s.docker(t, "port", s.id, "5432/tcp")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	for {
		conn, err := pgx.Connect(ctx, s.URL())
		if err == nil {
			_ = conn.Close(ctx)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("disposable PostgreSQL startup: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Server) URL() string { return s.endpoint.String() }

func (s *Server) Kill(t *testing.T) {
	t.Helper()
	s.docker(t, "kill", "--signal", "KILL", s.id)
	if code := s.docker(t, "inspect", "--format", "{{.State.ExitCode}}", s.id); code != "137" {
		t.Fatalf("engine exit: %s", code)
	}
}

func (s *Server) Restart(t *testing.T) {
	t.Helper()
	s.docker(t, "start", s.id)
	s.waitReady(t)
}
