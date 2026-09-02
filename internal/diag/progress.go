package diag

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// Progress renders a live stage panel to a writer during long commands.
//
// On a terminal it draws a stable N-line box, redrawing each stage line in
// place as state changes (pending → active → done/failed) and animating the
// active stage's spinner on a ticker. On a pipe, redirect, NO_COLOR, or a
// bytes.Buffer (tests), it falls back to one plain line per state change so
// output remains readable and machine-scrapeable.
//
// Callers must not write to the same writer while a Progress is active — the
// widget owns the last N lines. Close finalizes the panel and stops the
// spinner ticker.
type Progress struct {
	writer io.Writer
	tty    bool
	strong lipgloss.Style
	faint  lipgloss.Style
	good   lipgloss.Style
	bad    lipgloss.Style
	warn   lipgloss.Style

	mu         sync.Mutex
	stages     []stageState
	drawnLines int
	finished   bool
	spinner    int

	done chan struct{}
	tick *time.Ticker
}

type stageState struct {
	name     string
	status   stageStatus
	detail   string
	startAt  time.Time
	finishAt time.Time
}

type stageStatus int

const (
	stagePending stageStatus = iota
	stageActive
	stageDone
	stageFailed
	stageSkipped
)

// spinnerFrames is the same Braille rotator most modern CLIs use — one glyph
// wide in every terminal font, so redraw math stays trivial.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewProgress builds a Progress bound to writer. stageNames is the ordered list
// of labels the panel will render. If writer is not a real terminal (pipe,
// buffer, NO_COLOR, TERM=dumb), the panel degrades to a plain fallback.
func NewProgress(writer io.Writer, stageNames ...string) *Progress {
	renderer := lipgloss.NewRenderer(writer)
	if os.Getenv("TERM") == "dumb" {
		renderer.SetColorProfile(termenv.Ascii)
	}
	stages := make([]stageState, len(stageNames))
	for i, name := range stageNames {
		stages[i] = stageState{name: name, status: stagePending}
	}
	p := &Progress{
		writer: writer,
		tty:    isTerminalWriter(writer) && os.Getenv("TERM") != "dumb" && os.Getenv("NO_COLOR") == "",
		strong: renderer.NewStyle().Bold(true),
		faint:  renderer.NewStyle().Faint(true),
		good:   renderer.NewStyle().Foreground(lipgloss.Color("2")).Bold(true),
		bad:    renderer.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		warn:   renderer.NewStyle().Foreground(lipgloss.Color("3")),
		stages: stages,
	}
	if p.tty {
		p.start()
	}
	return p
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(file.Fd())
}

func (p *Progress) start() {
	p.tick = time.NewTicker(100 * time.Millisecond)
	p.done = make(chan struct{})
	go func() {
		for {
			select {
			case <-p.done:
				return
			case <-p.tick.C:
				p.mu.Lock()
				if !p.finished {
					p.spinner++
					p.redrawLocked()
				}
				p.mu.Unlock()
			}
		}
	}()
}

// Start marks the previous stage done (if any) and moves the spinner to name.
// Unknown names are a no-op — the panel only tracks stages declared in
// NewProgress.
func (p *Progress) Start(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	found := false
	for i := range p.stages {
		if p.stages[i].name != name {
			continue
		}
		found = true
		for j := 0; j < i; j++ {
			if p.stages[j].status == stagePending {
				p.stages[j].status = stageSkipped
			}
			if p.stages[j].status == stageActive {
				p.stages[j].status = stageDone
				p.stages[j].finishAt = now
			}
		}
		p.stages[i].status = stageActive
		p.stages[i].startAt = now
		p.stages[i].detail = ""
	}
	if !found {
		return
	}
	if p.tty {
		p.redrawLocked()
	} else {
		p.emitFallbackLine(name, "start", "")
	}
}

// Detail updates the trailing note on the active stage (e.g. current build
// step, "42 files packaged"). Empty strings clear the note.
func (p *Progress) Detail(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.stages {
		if p.stages[i].status == stageActive {
			p.stages[i].detail = text
			if p.tty {
				p.redrawLocked()
			}
			return
		}
	}
}

// StageDone finalizes name as complete. If detail is set, it replaces the
// stage's trailing note in the final rendering. Unknown names are a no-op.
func (p *Progress) StageDone(name, detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	found := false
	for i := range p.stages {
		if p.stages[i].name != name {
			continue
		}
		found = true
		p.stages[i].status = stageDone
		p.stages[i].finishAt = time.Now()
		if detail != "" {
			p.stages[i].detail = detail
		}
	}
	if !found {
		return
	}
	if p.tty {
		p.redrawLocked()
	} else {
		p.emitFallbackLine(name, "done", detail)
	}
}

// Finish marks every non-terminal stage as done, prints an optional final
// line beneath the panel, and stops the spinner.
func (p *Progress) Finish(finalLine string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finalizeLocked(true)
	if p.tty {
		p.redrawLocked()
	}
	if finalLine != "" {
		_, _ = fmt.Fprintln(p.writer, finalLine)
	}
}

// Fail marks the current active stage failed, all subsequent stages skipped,
// and stops the spinner. The error is not printed — the caller renders the
// diagnostic separately, since diag.Render handles that with full facts/hints.
func (p *Progress) Fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for i := range p.stages {
		switch p.stages[i].status {
		case stageActive:
			p.stages[i].status = stageFailed
			p.stages[i].finishAt = now
			if err != nil {
				p.stages[i].detail = err.Error()
			}
		case stagePending:
			p.stages[i].status = stageSkipped
		}
	}
	p.finalizeLocked(false)
	if p.tty {
		p.redrawLocked()
	} else if err != nil {
		_, _ = fmt.Fprintf(p.writer, "%s: failed: %v\n", p.currentStageNameLocked(), err)
	}
}

// Close stops the ticker if it is still running. Safe to call multiple times.
func (p *Progress) Close() {
	p.mu.Lock()
	if !p.finished {
		p.finalizeLocked(false)
		if p.tty {
			p.redrawLocked()
		}
	}
	p.mu.Unlock()
	if p.tick != nil {
		p.tick.Stop()
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	}
}

func (p *Progress) finalizeLocked(allDone bool) {
	p.finished = true
	now := time.Now()
	for i := range p.stages {
		if p.stages[i].status == stageActive {
			if allDone {
				p.stages[i].status = stageDone
			} else {
				p.stages[i].status = stageSkipped
			}
			p.stages[i].finishAt = now
		}
		if allDone && p.stages[i].status == stagePending {
			p.stages[i].status = stageSkipped
		}
	}
}

func (p *Progress) currentStageNameLocked() string {
	for _, stage := range p.stages {
		if stage.status == stageActive || stage.status == stageFailed {
			return stage.name
		}
	}
	if len(p.stages) > 0 {
		return p.stages[0].name
	}
	return ""
}

func (p *Progress) emitFallbackLine(stage, event, detail string) {
	if event == "start" {
		if detail == "" {
			_, _ = fmt.Fprintf(p.writer, "%s: %s\n", stage, "started")
		} else {
			_, _ = fmt.Fprintf(p.writer, "%s: %s (%s)\n", stage, "started", detail)
		}
		return
	}
	if detail == "" {
		_, _ = fmt.Fprintf(p.writer, "%s: %s\n", stage, event)
	} else {
		_, _ = fmt.Fprintf(p.writer, "%s: %s — %s\n", stage, event, detail)
	}
}

// redrawLocked repaints the panel in place. Caller must hold p.mu.
func (p *Progress) redrawLocked() {
	if !p.tty {
		return
	}
	// Move cursor up over the previously drawn lines and clear them, then
	// print the fresh panel. Using \r + cursor-up is portable across the
	// terminals shed targets; lipgloss handles color capability separately.
	var buf strings.Builder
	if p.drawnLines > 0 {
		fmt.Fprintf(&buf, "\r\x1b[%dA", p.drawnLines)
		for i := 0; i < p.drawnLines; i++ {
			buf.WriteString("\x1b[2K")
			if i < p.drawnLines-1 {
				buf.WriteString("\x1b[1B")
			}
		}
		fmt.Fprintf(&buf, "\r\x1b[%dA", p.drawnLines-1)
	}
	spinner := spinnerFrames[p.spinner%len(spinnerFrames)]
	lines := 0
	for _, stage := range p.stages {
		var symbol, name, suffix string
		switch stage.status {
		case stagePending:
			symbol = p.faint.Render("○")
			name = p.faint.Render(stage.name)
		case stageActive:
			symbol = p.warn.Render(spinner)
			name = p.strong.Render(stage.name)
			if stage.detail != "" {
				suffix = " " + p.faint.Render(stage.detail)
			}
		case stageDone:
			symbol = p.good.Render("●")
			name = stage.name
			if d := formatStageElapsed(stage); d != "" {
				suffix = " " + p.faint.Render(d)
			}
		case stageFailed:
			symbol = p.bad.Render("✗")
			name = p.bad.Render(stage.name)
			if stage.detail != "" {
				suffix = " " + p.bad.Render(stage.detail)
			}
		case stageSkipped:
			symbol = p.faint.Render("–")
			name = p.faint.Render(stage.name)
		}
		fmt.Fprintf(&buf, "  %s  %s%s\n", symbol, name, suffix)
		lines++
	}
	p.drawnLines = lines
	_, _ = io.WriteString(p.writer, buf.String())
}

func formatStageElapsed(stage stageState) string {
	if stage.startAt.IsZero() || stage.finishAt.IsZero() {
		return ""
	}
	elapsed := stage.finishAt.Sub(stage.startAt).Round(time.Millisecond)
	if elapsed < time.Second {
		return elapsed.String()
	}
	return elapsed.Round(100 * time.Millisecond).String()
}

// ProgressWriter returns an io.Writer that appends each non-empty line to the
// active stage's detail — handy for piping a builder's log stream into the
// panel without exposing the mutex.
func (p *Progress) LogWriter() io.Writer {
	return &progressLogWriter{p: p}
}

type progressLogWriter struct {
	p        *Progress
	overflow string
}

func (w *progressLogWriter) Write(payload []byte) (int, error) {
	if w.p == nil {
		return len(payload), nil
	}
	text := w.overflow + string(payload)
	w.overflow = ""
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && !strings.HasSuffix(text, "\n") {
		w.overflow = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			w.p.Detail(trimmed)
		}
	}
	return len(payload), nil
}

// ErrProgressAborted is returned by callers who observed a Progress.Close on
// cancellation and want to distinguish that from a real backend failure.
var ErrProgressAborted = errors.New("progress aborted")
