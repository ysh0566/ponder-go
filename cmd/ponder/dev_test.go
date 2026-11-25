package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDevReload(t *testing.T) {
	program := func(version string) string {
		return fmt.Sprintf(`package main
import("os";"os/signal";"fmt")
func main(){
	c:=make(chan os.Signal,1)
	signal.Notify(c,os.Interrupt)
	f,e:=os.OpenFile("runs",os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{panic(e)}
	fmt.Fprintln(f,%q,os.Getenv("PONDER_COMMAND"),os.Getenv("PONDER_SHADOW_SCHEMA"),os.Getenv("DATABASE_SCHEMA"),os.Getenv("PONDER_HOSTNAME"),os.Getenv("PONDER_PORT"));f.Close()
	<-c
	if e=os.WriteFile("closed-"+%q,nil,0600);e!=nil{panic(e)}
}`, version, version)
	}
	root := writeApplication(t, program("first"))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"dev", "--root", root, "--schema", "ponder_go_reloads", "--hostname", "127.0.0.1", "--port", "0"}, io.Discard)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	await := func(n int) []string {
		t.Helper()
		for ctx.Err() == nil {
			data, _ := os.ReadFile(filepath.Join(root, "runs"))
			lines := strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' })
			if len(lines) >= n {
				return lines
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("expected %d launches", n)
		return nil
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first := strings.Fields(await(1)[0])
	write("main.go", "broken")
	time.Sleep(time.Second)
	if _, err := os.Stat(filepath.Join(root, "closed-first")); !os.IsNotExist(err) {
		t.Fatal("a broken edit stopped the live child", err)
	}
	write("main.go", program("second"))
	second := strings.Fields(await(2)[1])
	if _, err := os.Stat(filepath.Join(root, "closed-first")); err != nil {
		t.Fatal("previous child did not stop before the replacement", err)
	}
	write("schema.json", `{"tables":[]}`)
	third := strings.Fields(await(3)[2])
	for _, fields := range [][]string{first, second, third} {
		if len(fields) != 6 || fields[1] != "dev" || !strings.HasPrefix(fields[2], "ponder_go_reloads_") || fields[2] != fields[3] || fields[4] != "127.0.0.1" || fields[5] != "0" {
			t.Fatalf("child environment %v", fields)
		}
	}
	if first[0] != "first" || second[0] != "second" || third[0] != "second" || first[2] == second[2] || second[2] == third[2] {
		t.Fatal(first, second, third)
	}
}

func TestLaunchAddressFlags(t *testing.T) {
	root := writeApplication(t, `package main
import("fmt";"os")
func main(){fmt.Print(os.Getenv("PONDER_HOSTNAME"),"/",os.Getenv("PONDER_PORT"))}`)
	for _, mode := range []string{"start", "serve"} {
		var out strings.Builder
		if err := run(t.Context(), []string{mode, "--root", root, "--port", "0", "--hostname", "::1"}, &out); err != nil || out.String() != "::1/0" {
			t.Fatal(mode, err, out.String())
		}
		if err := run(t.Context(), []string{mode, "--root", root, "--port", "65536"}, io.Discard); err == nil {
			t.Fatal(mode, "accepted invalid port")
		}
	}
}
