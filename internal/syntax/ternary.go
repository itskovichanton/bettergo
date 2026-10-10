// Package syntax describes BetterGo extensions on top of go/ast.
// A ternary "then if cond else els" is stored as a call of TernaryFunc
// so the rest of the tree stays ordinary go/ast.
package syntax

import "go/ast"

// TernaryFunc is the sentinel name of a parsed ternary call.
// Args are (then, cond, else), source order.
const TernaryFunc = "__bgoTernary"

// As reports whether e is a ternary and returns its three operands.
// Parentheses around the call are ignored.
func As(e ast.Expr) (then, cond, els ast.Expr, ok bool) {
	call, ok := Call(e)
	if !ok {
		return nil, nil, nil, false
	}
	return call.Args[0], call.Args[1], call.Args[2], true
}

// Call returns the sentinel call, ignoring surrounding parentheses.
func Call(e ast.Expr) (*ast.CallExpr, bool) {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 3 {
		return nil, false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != TernaryFunc {
		return nil, false
	}
	return call, true
}
