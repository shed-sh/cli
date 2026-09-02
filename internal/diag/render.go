package diag

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// Styler decides how much decoration a particular writer can carry. lipgloss
// inspects the writer itself, so a pipe, a redirect, NO_COLOR, and the buffers
// used by tests all degrade to plain text without a second switch and without
// the caller having to ask; TERM=dumb is handled below. Every command prints
// through one, so nothing has to remember whether the far end is a terminal.
type Styler struct {
	strong lipgloss.Style
	faint  lipgloss.Style
	width  int
}

func NewStyler(w io.Writer) Styler {
	renderer := lipgloss.NewRenderer(w)
	// TERM=dumb is a terminal that asked for no sequences, but the profile
	// detection only reads it to skip its own status queries, so decoration
	// survives unless it is turned off here.
	if os.Getenv("TERM") == "dumb" {
		renderer.SetColorProfile(termenv.Ascii)
	}
	return Styler{
		strong: renderer.NewStyle().Bold(true),
		faint:  renderer.NewStyle().Faint(true),
		width:  terminalWidth(w),
	}
}

// Strong marks a heading or the one sentence that carries the outcome.
func (s Styler) Strong(text string) string { return s.strong.Render(text) }

// Faint marks a label whose value beside it is the part worth reading.
func (s Styler) Faint(text string) string { return s.faint.Render(text) }

// Width is the column the caller should wrap prose to.
func (s Styler) Width() int { return s.width }

// terminalWidth caps the line length. Diagnostics are prose, and prose set to
// the full width of a maximised terminal is harder to read than prose set to a
// column, so the measured width is only ever used to make lines shorter.
func terminalWidth(w io.Writer) int {
	const (
		fallback  = 80
		narrowest = 40
		widest    = 96
	)
	file, isFile := w.(*os.File)
	if !isFile {
		return fallback
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width < narrowest {
		return fallback
	}
	return min(width, widest)
}

// Render writes the diagnostic as guidance: summary, evidence, next steps.
func Render(w io.Writer, e *Error) {
	if e == nil {
		return
	}
	style := NewStyler(w)
	_, _ = fmt.Fprintln(w, style.Strong(e.Error()))

	if len(e.Facts) > 0 {
		_, _ = fmt.Fprintln(w)
		RenderFacts(w, e.Facts)
	}

	if len(e.Hints) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, style.Strong("Next steps:"))
		for _, hint := range e.Hints {
			for index, line := range wrap(hint, style.Width()-4) {
				prefix := "  "
				if index > 0 {
					prefix = "    "
				}
				_, _ = fmt.Fprintf(w, "%s%s\n", prefix, line)
			}
		}
	}
}

// RenderFacts writes an aligned label/value block. A fact with an empty label
// continues the one above it, which is how a list of build commands stays under
// a single heading instead of repeating it.
func RenderFacts(w io.Writer, facts []Fact) {
	if len(facts) == 0 {
		return
	}
	style := NewStyler(w)
	labelWidth := 0
	for _, fact := range facts {
		if width := lipgloss.Width(fact.Label); width > labelWidth {
			labelWidth = width
		}
	}
	for _, fact := range facts {
		for index, line := range wrap(fact.Value, style.Width()-labelWidth-6) {
			label := strings.Repeat(" ", labelWidth)
			if index == 0 && fact.Label != "" {
				label = style.Faint(fact.Label) + strings.Repeat(" ", labelWidth-lipgloss.Width(fact.Label))
			}
			_, _ = fmt.Fprintf(w, "  %s  %s\n", label, line)
		}
	}
}

// wrap breaks plain text on spaces. Everything reaching it is unstyled — styling
// is applied to labels only, after wrapping — so counting bytes is exact for the
// ASCII prose these diagnostics carry.
func wrap(text string, width int) []string {
	words := strings.Fields(text)
	if width < 24 || len(words) == 0 {
		return []string{text}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			lines, current = append(lines, current), word
			continue
		}
		current += " " + word
	}
	return append(lines, current)
}
