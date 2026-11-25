package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ysh0566/ponder-go/internal/devstatus"
	"golang.org/x/term"
)

// The dashboard owns only the lines below the most recent complete log line.
// Leave partial application writes alone until a newline puts the cursor back
// at the start of a fresh line.
type devDashboard struct {
	mu      sync.Mutex
	tty     *os.File
	file    string
	message string
	report  devstatus.Snapshot
	drawn   string
	rows    int
	partial bool
}

func newDevDashboard(stdout, stderr io.Writer, path string) *devDashboard {
	out, outOK := stdout.(*os.File)
	tty, ttyOK := stderr.(*os.File)
	if !outOK || !ttyOK || os.Getenv("TERM") == "dumb" || !term.IsTerminal(int(tty.Fd())) {
		return nil
	}
	a, err := out.Stat()
	b, otherErr := tty.Stat()
	if err != nil || otherErr != nil || !os.SameFile(a, b) {
		return nil
	}
	return &devDashboard{tty: tty, file: path}
}

func (d *devDashboard) clear() {
	if d.rows > 0 {
		fmt.Fprintf(d.tty, "\r\x1b[%dA\x1b[J", d.rows)
		d.rows, d.drawn = 0, ""
	}
}

func (d *devDashboard) lines(columns, rows int) []string {
	if columns < 2 || rows < 2 {
		return nil
	}
	lines := []string{"Ponder dev | " + d.message}
	report := &d.report
	if report.Address != "" {
		status := "Syncing"
		switch {
		case report.ReadinessUnavailable:
			status = "Readiness unavailable"
		case report.Ready:
			status = "Ready"
		}
		lines = append(lines, status+" | http://"+report.Address)
		switch {
		case report.Blocks == nil:
			lines = append(lines, "Checkpoints unavailable")
		case len(report.Blocks) == 0:
			lines = append(lines, "Waiting for first checkpoint")
		default:
			for _, name := range slices.Sorted(maps.Keys(report.Blocks)) {
				// Keep terminal controls and wide characters out of cursor arithmetic.
				quoted := strconv.QuoteToASCII(name)
				lines = append(lines, fmt.Sprintf("%s  block %d", quoted[1:len(quoted)-1], report.Blocks[name]))
			}
		}
	}
	if len(lines) > rows-1 {
		n := rows - 1
		if n > 2 {
			lines[n-1] = fmt.Sprintf("+%d more", len(lines)-n+1)
		}
		lines = lines[:n]
	}
	for i, line := range lines {
		lines[i] = line[:min(len(line), columns-1)]
	}
	return lines
}

func (d *devDashboard) draw() {
	if d.partial || d.message == "" {
		return
	}
	width, height, err := term.GetSize(int(d.tty.Fd()))
	if err != nil {
		d.clear()
		return
	}
	lines := d.lines(width, height)
	frame := strings.Join(lines, "\r\n")
	if frame == d.drawn {
		return
	}
	d.clear()
	if len(lines) > 0 {
		fmt.Fprint(d.tty, frame, "\r\n")
		d.rows, d.drawn = len(lines), frame
	}
}

func (d *devDashboard) update(message string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.message = message
	d.report = devstatus.Snapshot{}
	if data, err := os.ReadFile(d.file); err == nil {
		var report devstatus.Snapshot
		if json.Unmarshal(data, &report) == nil {
			d.report = report
		}
	}
	d.draw()
}

func (d *devDashboard) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clear()
	d.message = ""
}

type devOutput struct {
	dashboard *devDashboard
	out       io.Writer
}

func (w devOutput) Write(p []byte) (int, error) {
	d := w.dashboard
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	d.clear()
	n, err := w.out.Write(p)
	if n > 0 {
		d.partial = p[n-1] != '\n'
	}
	d.draw()
	return n, err
}
