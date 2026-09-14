// Proof-of-bug: connection drain missing in cloudflare FetchZones paginated loop.
package cloudflare

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestFetchZonesBodyDrained(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "handler.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse handler.go: %v", err)
	}

	// Find FetchZonesWithClient
	var fetchZonesBody *ast.BlockStmt
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "FetchZonesWithClient" {
			fetchZonesBody = fn.Body
			break
		}
	}
	if fetchZonesBody == nil {
		t.Fatal("FetchZonesWithClient not found in handler.go")
	}

	// Recursively walk every statement in the function body.
	// For each resp.Body.Close() found, the IMMEDIATE previous statement
	// (sibling in the same BlockStmt) must be io.Copy(io.Discard, resp.Body).
	broken := false
	var visitBlock func(list []ast.Stmt)
	visitBlock = func(list []ast.Stmt) {
		for i, stmt := range list {
			// Recurse into nested blocks (if/for/if bodies)
			switch v := stmt.(type) {
			case *ast.ForStmt:
				if v.Body != nil {
					visitBlock(v.Body.List)
				}
			case *ast.IfStmt:
				if v.Body != nil {
					visitBlock(v.Body.List)
				}
				if v.Else != nil {
					if blk, ok := v.Else.(*ast.BlockStmt); ok {
						visitBlock(blk.List)
					}
				}
			case *ast.RangeStmt:
				if v.Body != nil {
					visitBlock(v.Body.List)
				}
			case *ast.SwitchStmt:
				for _, c := range v.Body.List {
					if cs, ok := c.(*ast.CaseClause); ok {
						visitBlock(cs.Body)
					}
				}
			case *ast.SelectStmt:
				for _, c := range v.Body.List {
					if cs, ok := c.(*ast.CommClause); ok {
						visitBlock(cs.Body)
					}
				}
			}

			// Check for resp.Body.Close() at top-level of this block
			ast.Inspect(stmt, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Close" {
					return true
				}
				x, ok := sel.X.(*ast.Ident)
				if !ok || x.Name != "resp" {
					return true
				}
				// Found resp.Body.Close(). Check if prev sibling is io.Copy(io.Discard, resp.Body)
				if i == 0 || !isIoCopyDrain(list[i-1]) {
					t.Logf("FAIL: resp.Body.Close() at %s has no preceding io.Copy(io.Discard, resp.Body)",
						fset.Position(sel.Pos()))
					broken = true
				}
				return false
			})
		}
	}

	visitBlock(fetchZonesBody.List)

	if broken {
		t.Fatal("FAIL: FetchZonesWithClient has resp.Body.Close() without io.Copy(io.Discard, resp.Body) — HTTP/1.1 keep-alive broken on non-2xx responses")
	}
	t.Log("PASS: all resp.Body.Close() calls in FetchZonesWithClient are preceded by io.Copy(io.Discard, resp.Body)")
}

func isIoCopyDrain(stmt ast.Stmt) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Copy" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "io" {
		return false
	}
	if len(call.Args) < 2 {
		return false
	}
	_, ok = call.Args[1].(*ast.SelectorExpr)
	return ok
}
