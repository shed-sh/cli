// Package diag carries user-facing failures. A command returns a diagnostic
// whenever the person running it can do something about the problem: it pairs a
// one-line summary with the facts behind it and the next steps that resolve it,
// so the CLI can print guidance instead of a bare error string.
package diag

import (
	"errors"
)

// Fact is one label/value pair printed in the aligned evidence block.
type Fact struct {
	Label string
	Value string
}

// Error is a diagnostic. Summary is what Error reports, so machine output stays
// a single line; Facts and Hints are human-only detail.
type Error struct {
	// Code is the stable identifier reported in --output json.
	Code string
	// Summary states the problem in one sentence.
	Summary string
	// Facts are the evidence behind the summary.
	Facts []Fact
	// Hints are concrete next steps, one per line.
	Hints []string
	// Cause is the underlying error, when one exists.
	Cause error
}

func (e *Error) Error() string {
	if e.Summary == "" && e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Summary
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// As reports the diagnostic carried anywhere in err's chain.
func As(err error) (*Error, bool) {
	var diagnostic *Error
	if errors.As(err, &diagnostic) {
		return diagnostic, true
	}
	return nil, false
}
