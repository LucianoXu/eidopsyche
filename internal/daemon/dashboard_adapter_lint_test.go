package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestDashboardAdapter_AllMethodsRouteThroughCall is the Phase 6
// lock-down: it parses dashboard_adapter.go and asserts that every
// method on dashboardAdapter satisfies one of:
//
//   - the body contains a *Daemon.Call(...) invocation (the unified
//     call path), or
//   - the body is the explicit streaming exception
//     a.d.subscribeDashboard() (per design §4.1), or
//   - the method's doc comment carries "adapter-shim:" — the visible
//     marker that the author opted out, with a one-line rationale
//     immediately following.
//
// Without this test, future contributors can quietly reintroduce
// direct a.d.Repo / a.d.DB / a.d.X access — the very drift PR #25
// (Phase 1) and PRs #26–#29 (Phases 2–5) eliminated. The test runs
// under `go test ./internal/daemon/` so it gates merge in CI.
//
// See docs/superpowers/specs/2026-05-09-unified-call-path-design.md §6
// Phase 6.
func TestDashboardAdapter_AllMethodsRouteThroughCall(t *testing.T) {
	const path = "dashboard_adapter.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !isDashboardAdapterMethod(fn) {
			continue
		}
		if isAdapterShim(fn) {
			continue
		}
		if usesCallOrStreamingException(fn) {
			continue
		}
		pos := fset.Position(fn.Pos())
		violations = append(violations,
			pos.String()+": "+fn.Name.Name+
				" must route through (*Daemon).Call, use subscribeDashboard for streaming, or carry an // adapter-shim: marker comment")
	}

	if len(violations) > 0 {
		t.Fatalf("unified-call-path violations:\n  %s\n\n"+
			"Fix: route the method through a.d.Call(ctx, \"<method>\", params, &out), "+
			"or — if the method legitimately cannot — add an `// adapter-shim:` line to "+
			"its doc comment with a one-line rationale.",
			strings.Join(violations, "\n  "))
	}
}

// isDashboardAdapterMethod reports whether decl is a method declared
// on the dashboardAdapter receiver type. Both value and pointer
// receivers are accepted so the test stays robust to receiver-type
// refactors.
func isDashboardAdapterMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return false
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "dashboardAdapter"
}

// isAdapterShim reports whether the method's doc comment contains the
// "adapter-shim:" marker.
func isAdapterShim(fn *ast.FuncDecl) bool {
	if fn.Doc == nil {
		return false
	}
	for _, c := range fn.Doc.List {
		if strings.Contains(c.Text, "adapter-shim:") {
			return true
		}
	}
	return false
}

// usesCallOrStreamingException walks the function body and reports
// whether the only daemon-internal accesses are via Call or the
// whitelisted streaming helper.
//
// Specifically: every selector chain rooted at the receiver's `.d`
// field (e.g. `a.d.Repo`, `a.d.Call`, `a.d.subscribeDashboard`) must
// terminate in one of the allowed names. Any other `a.d.<X>` access —
// `a.d.Key.PublicHex`, `a.d.Repo.Get`, direct field reads — fails the
// gate. Shims that legitimately need richer access mark themselves
// with `adapter-shim:` and skip this check entirely.
//
// The receiver name is read off the function's receiver declaration
// rather than hardcoded so this stays robust to a future rename.
func usesCallOrStreamingException(fn *ast.FuncDecl) bool {
	if fn.Body == nil || fn.Recv == nil || len(fn.Recv.List) == 0 ||
		len(fn.Recv.List[0].Names) == 0 {
		return false
	}
	recvName := fn.Recv.List[0].Names[0].Name

	allowed := map[string]bool{
		"Call":               true,
		"subscribeDashboard": true,
	}
	hasAtLeastOne := false
	clean := true

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// Look for selector on the receiver's `.d` — i.e. the inner
		// expression must be `<recv>.d`.
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := inner.X.(*ast.Ident)
		if !ok || ident.Name != recvName || inner.Sel.Name != "d" {
			return true
		}
		if allowed[sel.Sel.Name] {
			hasAtLeastOne = true
			return true
		}
		clean = false
		return true
	})
	return hasAtLeastOne && clean
}
