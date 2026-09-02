package docs

import (
	"strings"
	"testing"

	"shed/internal/clispec"
	"shed/internal/shedfile"
)

func TestInjectBlocksReplacesOnlyMarkedRegion(t *testing.T) {
	content := strings.Join([]string{
		"# Title",
		"",
		"Hand-written prose above.",
		"<!-- BEGIN GENERATED: commands -->",
		"stale content",
		"more stale content",
		"<!-- END GENERATED: commands -->",
		"",
		"Hand-written prose below.",
	}, "\n")

	got, err := InjectBlocks(content, map[string]string{"commands": "fresh content"})
	if err != nil {
		t.Fatalf("InjectBlocks() error = %v", err)
	}

	for _, want := range []string{"Hand-written prose above.", "Hand-written prose below.", "fresh content"} {
		if !strings.Contains(got, want) {
			t.Errorf("InjectBlocks() dropped %q", want)
		}
	}
	if strings.Contains(got, "stale content") {
		t.Error("InjectBlocks() left the previous generated content behind")
	}
}

// TestInjectBlocksIsIdempotent guards the failure mode that makes generated
// documentation untrustworthy: a second run that appends rather than replaces.
func TestInjectBlocksIsIdempotent(t *testing.T) {
	content := "<!-- BEGIN GENERATED: commands -->\nold\n<!-- END GENERATED: commands -->\n"

	once, err := InjectBlocks(content, map[string]string{"commands": "new"})
	if err != nil {
		t.Fatalf("InjectBlocks() error = %v", err)
	}
	twice, err := InjectBlocks(once, map[string]string{"commands": "new"})
	if err != nil {
		t.Fatalf("InjectBlocks() second pass error = %v", err)
	}
	if once != twice {
		t.Errorf("InjectBlocks() is not idempotent:\nfirst:  %q\nsecond: %q", once, twice)
	}
}

func TestInjectBlocksRejectsBadMarkers(t *testing.T) {
	for name, content := range map[string]string{
		"missing":      "# Title\n\nNo markers at all.\n",
		"unterminated": "<!-- BEGIN GENERATED: commands -->\nbody\n",
		"duplicated": "<!-- BEGIN GENERATED: commands -->\na\n<!-- END GENERATED: commands -->\n" +
			"<!-- BEGIN GENERATED: commands -->\nb\n<!-- END GENERATED: commands -->\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := InjectBlocks(content, map[string]string{"commands": "new"}); err == nil {
				t.Error("InjectBlocks() accepted a malformed marker set; a silent append would corrupt the file")
			}
		})
	}
}

// TestCommandsReferenceDocumentsEveryFlag is the whole point of generating this
// file: the reference cannot fall behind the registry the parser uses.
func TestCommandsReferenceDocumentsEveryFlag(t *testing.T) {
	reference, err := RenderCommandsReference()
	if err != nil {
		t.Fatalf("RenderCommandsReference() error = %v", err)
	}
	for _, command := range clispec.Commands() {
		if !strings.Contains(reference, "### `shed "+command.Signature()+"`") {
			t.Errorf("reference has no section for %q", command.Name)
		}
		for _, declared := range command.Flags {
			if !strings.Contains(reference, "`--"+declared.Name) {
				t.Errorf("reference never documents %s --%s", command.Name, declared.Name)
			}
		}
	}
}

// TestStarlarkReferenceDocumentsEveryBuiltin mirrors the commands guarantee
// for the language surface: every builtin the evaluator accepts, and every
// argument of each, has a place in the reference — including the boundary
// statement of what is not yet supported.
func TestStarlarkReferenceDocumentsEveryBuiltin(t *testing.T) {
	reference, err := RenderStarlarkReference()
	if err != nil {
		t.Fatalf("RenderStarlarkReference() error = %v", err)
	}
	schema := shedfile.APISchema()
	for _, builtin := range schema.Builtins {
		if !strings.Contains(reference, "### `"+builtin.Signature+"`") {
			t.Errorf("reference has no section for %s()", builtin.Name)
		}
		for _, arg := range builtin.Args {
			if !strings.Contains(reference, "| `"+arg.Name+"` |") {
				t.Errorf("reference never documents %s() argument %q", builtin.Name, arg.Name)
			}
		}
	}
	for _, absent := range schema.NotYetSupported {
		if !strings.Contains(reference, absent) {
			t.Errorf("reference never states that %s is not yet supported", absent)
		}
	}
}

// TestAgentContextCarriesEveryDocument guards what llms-full.txt is for: one
// file an agent can be handed whole, with nothing left behind in the others.
func TestAgentContextCarriesEveryDocument(t *testing.T) {
	rendered := RenderAgentContext(AgentContext{
		Skill:    "---\nname: shed\ndescription: Deploy things simply. Trigger on every shed mention.\n---\n\n# shed\n\nSkill body prose.\n",
		Starlark: "# Starlark reference\n\nLanguage prose.\n",
		Schema:   "# Schema reference\n\nSchema prose.\n",
		Commands: "# Commands reference\n\nCommand prose.\n",
		Errors:   "# Error reference\n\nError prose.\n",
	})

	for _, want := range []string{
		"> Deploy things simply.",
		"Skill body prose.", "Language prose.", "Schema prose.", "Command prose.", "Error prose.",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("agent context dropped %q", want)
		}
	}
	if strings.Contains(rendered, "Trigger on every shed mention") {
		t.Error("agent context kept the skill-routing half of the description")
	}
	if strings.Contains(rendered, "name: shed") {
		t.Error("agent context kept the skill frontmatter")
	}
}

// TestCommandsReferenceEscapesTableCells pins the escaping that keeps enum
// values such as human|json|ndjson from splitting a markdown table row.
func TestCommandsReferenceEscapesTableCells(t *testing.T) {
	reference, err := RenderCommandsReference()
	if err != nil {
		t.Fatalf("RenderCommandsReference() error = %v", err)
	}
	if !strings.Contains(reference, `--output human\|json\|ndjson`) {
		t.Error("reference did not escape the pipes in an --output value")
	}
}
