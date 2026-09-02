package diag

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorReportsOnlyTheSummarySoMachineOutputStaysOneLine(t *testing.T) {
	failure := &Error{
		Code:    "unsupported_project",
		Summary: "Detected Go, which Shed cannot build automatically yet.",
		Facts:   []Fact{{Label: "Supported", Value: "Node.js"}},
		Hints:   []string{"Write SHED.yaml by hand"},
	}

	if got := failure.Error(); got != failure.Summary {
		t.Fatalf("Error() = %q", got)
	}
	if strings.Contains(failure.Error(), "\n") {
		t.Fatalf("Error() spans lines: %q", failure.Error())
	}
}

func TestAsFindsTheDiagnosticThroughWrapping(t *testing.T) {
	failure := &Error{Code: "runtime_unavailable", Summary: "Docker was not found.", Cause: errors.New("exec: not found")}
	wrapped := fmt.Errorf("build image: %w", failure)

	found, ok := As(wrapped)
	if !ok || found.Code != "runtime_unavailable" {
		t.Fatalf("As() = %#v, %v", found, ok)
	}
	if !errors.Is(wrapped, failure) {
		t.Fatal("wrapped diagnostic lost its identity")
	}
}

func TestRenderWritesSummaryEvidenceAndNextSteps(t *testing.T) {
	var output bytes.Buffer

	Render(&output, &Error{
		Summary: "Detected Go, which Shed cannot build automatically yet.",
		Facts:   []Fact{{Label: "Supported", Value: "Node.js"}, {Label: "Directory", Value: "/tmp/app"}},
		Hints:   []string{"Write SHED.yaml by hand", "Then run: shed deploy"},
	})

	want := "Detected Go, which Shed cannot build automatically yet.\n" +
		"\n" +
		"  Supported  Node.js\n" +
		"  Directory  /tmp/app\n" +
		"\n" +
		"Next steps:\n" +
		"  Write SHED.yaml by hand\n" +
		"  Then run: shed deploy\n"
	if output.String() != want {
		t.Fatalf("Render() =\n%s\nwant\n%s", output.String(), want)
	}
}

// Anything that is not a terminal — a pipe, a redirect, a log collector, an
// agent reading the stream — must receive plain bytes with no escape sequences.
func TestRenderEmitsNoEscapeSequencesOffTerminal(t *testing.T) {
	var output bytes.Buffer

	Render(&output, &Error{
		Code:    "unsupported_project",
		Summary: "Detected Go, which Shed cannot build automatically yet.",
		Facts:   []Fact{{Label: "Supported", Value: "Node.js"}},
		Hints:   []string{"Write SHED.yaml by hand"},
	})

	if strings.ContainsRune(output.String(), '\x1b') {
		t.Fatalf("escape sequences reached a non-terminal writer: %q", output.String())
	}
}

// Long evidence must not run off a narrow terminal, and continuation lines must
// stay under their label rather than restarting at the margin.
func TestRenderWrapsLongValuesWithAHangingIndent(t *testing.T) {
	var output bytes.Buffer

	Render(&output, &Error{
		Summary: "Shed could not find an application to build here.",
		Facts: []Fact{
			{Label: "Railpack", Value: strings.Repeat("word ", 40)},
			{Label: "Dir", Value: "/tmp/app"},
		},
	})

	for _, line := range strings.Split(strings.TrimRight(output.String(), "\n"), "\n") {
		if len(line) > 96 {
			t.Fatalf("line exceeds the width cap (%d): %q", len(line), line)
		}
	}
	// "  " + "Railpack" + "  " puts values in column 12; continuations align there.
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "  ") && strings.Contains(line, "word") && !strings.HasPrefix(line, strings.Repeat(" ", 12)) {
			if !strings.HasPrefix(line, "  Railpack  ") {
				t.Fatalf("continuation line is not aligned under the value column: %q", line)
			}
		}
	}
}

func TestRenderOmitsEmptySections(t *testing.T) {
	var output bytes.Buffer

	Render(&output, &Error{Summary: "You are not signed in to Shed."})

	if output.String() != "You are not signed in to Shed.\n" {
		t.Fatalf("Render() = %q", output.String())
	}
}
