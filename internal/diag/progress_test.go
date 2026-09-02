package diag

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestProgressFallbackWritesLinePerTransition(t *testing.T) {
	// A non-TTY writer (bytes.Buffer) exercises the fallback path — the
	// widget must never draw ANSI escapes there.
	var buf bytes.Buffer
	p := NewProgress(&buf, "package", "build", "run")

	p.Start("package")
	p.StageDone("package", "42 files")
	p.Start("build")
	p.StageDone("build", "")
	p.Start("run")
	p.Finish("Ready: http://localhost:8080")

	output := buf.String()
	if strings.Contains(output, "\x1b[") {
		t.Fatalf("fallback path wrote ANSI escapes: %q", output)
	}
	for _, want := range []string{"package", "build", "run", "Ready: http://localhost:8080"} {
		if !strings.Contains(output, want) {
			t.Errorf("fallback output missing %q; got %q", want, output)
		}
	}
	if !strings.Contains(output, "42 files") {
		t.Errorf("fallback output dropped stage detail: %q", output)
	}
	p.Close()
}

func TestProgressFailPrintsFailureAndSkipsRest(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, "package", "build", "run")

	p.Start("package")
	p.StageDone("package", "")
	p.Start("build")
	p.Fail(errors.New("compile error"))
	p.Close()

	output := buf.String()
	if !strings.Contains(output, "build") || !strings.Contains(output, "failed") {
		t.Fatalf("expected build failure line, got %q", output)
	}
	if !strings.Contains(output, "compile error") {
		t.Fatalf("expected error message in fallback output, got %q", output)
	}
}

func TestProgressUnknownStageIsNoop(t *testing.T) {
	// Start with a name that was not declared must not panic and must not
	// affect the declared stages.
	var buf bytes.Buffer
	p := NewProgress(&buf, "package", "build")
	p.Start("verify")
	p.StageDone("verify", "")
	p.Finish("")
	p.Close()
	if strings.Contains(buf.String(), "verify") {
		t.Fatalf("unknown stage leaked into output: %q", buf.String())
	}
}

func TestProgressLogWriterFeedsActiveStage(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, "build")
	p.Start("build")
	// Simulate a builder streaming multi-line log output.
	if _, err := p.LogWriter().Write([]byte("step 1/3\nstep 2/3\n")); err != nil {
		t.Fatalf("LogWriter.Write: %v", err)
	}
	// Fallback path prints Detail() as nothing extra — it only shows up on
	// the redrawn TTY panel. So we assert no panic and the panel still ends
	// cleanly on Finish.
	p.Finish("done")
	p.Close()
	if !strings.Contains(buf.String(), "done") {
		t.Fatalf("progress did not emit final line: %q", buf.String())
	}
}

func TestProgressCloseIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, "one")
	p.Start("one")
	p.Close()
	p.Close() // must not panic or double-close the done channel
}
