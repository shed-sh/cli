package shedfile

import (
	"go.starlark.net/syntax"
)

// lint runs the structural checks that need the program's shape rather than
// its values. V1 has one: a build() call must be the entire right-hand side
// of an assignment, because http_app(build = b) passing a named value is the
// shape every future sharing feature builds on.
func (ev *evaluator) lint(file *syntax.File) {
	allowed := make(map[*syntax.CallExpr]struct{})
	syntax.Walk(file, func(node syntax.Node) bool {
		if assign, ok := node.(*syntax.AssignStmt); ok && assign.Op == syntax.EQ {
			if call, isCall := assign.RHS.(*syntax.CallExpr); isCall && isBuildCall(call) {
				allowed[call] = struct{}{}
			}
		}
		return true
	})
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || !isBuildCall(call) {
			return true
		}
		if _, fine := allowed[call]; !fine {
			start, _ := call.Span()
			ev.report(start, "inline_build", "build(...) may not be written inline.",
				"Assign it to a variable first: b = build(...), then http_app(build = b, ...)")
		}
		return true
	})
}

func isBuildCall(call *syntax.CallExpr) bool {
	ident, ok := call.Fn.(*syntax.Ident)
	return ok && ident.Name == "build"
}
