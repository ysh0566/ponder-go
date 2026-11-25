package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ysh0566/ponder-go/internal/devstatus"
	"github.com/ysh0566/ponder-go/metrics"
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
		if report.Blocks == nil {
			lines = append(lines, "Checkpoints unavailable")
		} else if len(report.Blocks) == 0 {
			lines = append(lines, "Waiting for first checkpoint")
		}
		names := make(map[string]bool, len(report.Chains)+len(report.Blocks))
		for name := range report.Chains {
			names[name] = true
		}
		for name := range report.Blocks {
			names[name] = true
		}
		for _, name := range slices.Sorted(maps.Keys(names)) {
			line := ""
			if n, ok := report.Blocks[name]; ok {
				line += fmt.Sprintf("  block %d", n)
			}
			if stats, ok := report.Chains[name]; ok {
				line += fmt.Sprintf("  RPC %.1f req/s", stats.RequestsPerSecond)
				if p := stats.Backfill; p != nil && p.Total > 0 {
					percent := 100.0
					if p.Completed < p.Total {
						percent = math.Min(99.9, math.Round(1000*float64(p.Completed)/float64(p.Total))/10)
					}
					line += fmt.Sprintf("  backfill %g%%", percent)
					if p.ETASeconds != nil {
						line += "  ETA " + displayETA(*p.ETASeconds)
					}
				}
			}
			lines = append(lines, metricLine(name, line, columns))
		}
		for _, name := range slices.Sorted(maps.Keys(report.Events)) {
			e := report.Events[name]
			line := fmt.Sprintf("  calls %d", e.Count)
			if e.Count > 0 {
				if e.AverageMillis > 0 && e.AverageMillis < 0.001 {
					line += "  avg <0.001 ms"
				} else {
					line += fmt.Sprintf("  avg %.3f ms", e.AverageMillis)
				}
			}
			lines = append(lines, metricLine(name, line, columns))
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

// Escaped ASCII keeps user-supplied labels within the cursor's column count.
func metricLine(name, values string, columns int) string {
	quoted := strconv.QuoteToASCII(name)
	limit := max(8, min(36, columns-1-len(values)))
	return metrics.Truncate(quoted[1:len(quoted)-1], limit) + values
}

func displayETA(seconds float64) string {
	if seconds >= 86400 {
		return fmt.Sprintf("%.0fd", math.Ceil(seconds/86400))
	}
	return (time.Duration(math.Ceil(seconds)) * time.Second).String()
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
