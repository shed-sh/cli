package shedfile

import (
	"fmt"
	"io"
	"strings"
)

// Schema is the machine-readable statement of the V1 surface, rendered from
// the same spec tables that validate calls, so it cannot drift from behavior.
type Schema struct {
	Type            string          `json:"type"`
	File            string          `json:"file"`
	Builtins        []SchemaBuiltin `json:"builtins"`
	NotYetSupported []string        `json:"notYetSupported"`
}

type SchemaBuiltin struct {
	Name      string      `json:"name"`
	Signature string      `json:"signature"`
	Doc       string      `json:"doc"`
	Args      []SchemaArg `json:"args"`
}

type SchemaArg struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
	Doc      string `json:"doc"`
}

func APISchema() Schema {
	schema := Schema{
		Type:            "schema",
		File:            FileName,
		NotYetSupported: append([]string(nil), notYetSupported...),
	}
	for _, spec := range builtinSpecs {
		entry := SchemaBuiltin{Name: spec.name, Signature: signature(spec), Doc: spec.doc}
		for _, arg := range spec.args {
			entry.Args = append(entry.Args, SchemaArg{
				Name:     arg.name,
				Type:     arg.kind.typeName(),
				Required: arg.required,
				Default:  arg.defaultLiteral,
				Doc:      arg.doc,
			})
		}
		schema.Builtins = append(schema.Builtins, entry)
	}
	return schema
}

// signature renders the call the way Python documentation would, with `*`
// marking where keyword-only arguments begin.
func signature(spec builtinSpec) string {
	var parts []string
	starred := false
	for _, arg := range spec.args {
		if !arg.positional && !starred {
			parts = append(parts, "*")
			starred = true
		}
		part := arg.name
		if arg.defaultLiteral != "" {
			part += " = " + arg.defaultLiteral
		}
		parts = append(parts, part)
	}
	return spec.name + "(" + strings.Join(parts, ", ") + ")"
}

// RenderSchema writes the human form: each signature, its purpose, and every
// argument with its one accepted representation.
func RenderSchema(w io.Writer) {
	_, _ = fmt.Fprintf(w, "The %s file API. These three functions are all that exist; any other name is an error.\n", FileName)
	for _, spec := range builtinSpecs {
		_, _ = fmt.Fprintf(w, "\n%s\n", signature(spec))
		_, _ = fmt.Fprintf(w, "  %s\n", spec.doc)
		width := 0
		for _, arg := range spec.args {
			if len(arg.name) > width {
				width = len(arg.name)
			}
		}
		for _, arg := range spec.args {
			requirement := "= " + arg.defaultLiteral
			if arg.required {
				requirement = "required"
			}
			_, _ = fmt.Fprintf(w, "    %-*s  %-13s %s\n", width, arg.name, requirement, arg.kind.typeName())
			_, _ = fmt.Fprintf(w, "    %-*s  %s\n", width, "", arg.doc)
		}
	}
	_, _ = fmt.Fprintf(w, "\nNot yet supported: %s.\n", strings.Join(notYetSupported, ", "))
}
