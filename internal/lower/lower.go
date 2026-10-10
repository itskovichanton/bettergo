// Package lower rewrites BetterGo ternary expressions into ordinary Go.
// Untaken branches are not evaluated. Positions of the original expressions
// are kept so go/printer SourcePos emits //line directives back to the .bgo file.
package lower

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"bettergo/internal/syntax"
)

var exprIface = reflect.TypeOf((*ast.Expr)(nil)).Elem()

// Error is a diagnostic tied to a source position.
type Error struct {
	Pos token.Position
	Msg string
}

func (e Error) Error() string {
	if e.Pos.Filename == "" {
		return e.Msg
	}
	return fmt.Sprintf("%s:%d:%d: %s", e.Pos.Filename, e.Pos.Line, e.Pos.Column, e.Msg)
}

// File rewrites ternaries in file. The file must have been parsed by the
// BetterGo parser. On success the AST is valid Go.
func File(fset *token.FileSet, file *ast.File) []Error {
	if err := rejectConst(fset, file); err != nil {
		return []Error{*err}
	}
	l := &lowerer{fset: fset, file: file, targets: map[*ast.CallExpr]types.Type{}}
	l.infer()
	l.walkDecls(file.Decls, false)
	return l.errs
}

func rejectConst(fset *token.FileSet, file *ast.File) *Error {
	var found *Error
	ast.Inspect(file, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST || found != nil {
			return found == nil
		}
		ast.Inspect(gd, func(n ast.Node) bool {
			e, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			if _, ok := syntax.Call(e); ok && found == nil {
				found = &Error{Pos: fset.Position(e.Pos()), Msg: "ternary is not a constant"}
			}
			return found == nil
		})
		return true
	})
	return found
}

type lowerer struct {
	fset    *token.FileSet
	file    *ast.File
	info    *types.Info
	pkg     *types.Package
	sig     *types.Signature
	targets map[*ast.CallExpr]types.Type
	idents  map[string]types.Type
	errs    []Error
	tmp     int
}

func (l *lowerer) infer() {
	parents := parentMap(l.file)
	var outermost []*ast.CallExpr
	ast.Inspect(l.file, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		call, ok := syntax.Call(e)
		if !ok || call != e && !isParenOf(e, call) {
			return true
		}
		// Only the call node itself is recorded once.
		if n != call {
			return true
		}
		if nestedInTernary(parents, call) {
			return true
		}
		outermost = append(outermost, call)
		return true
	})
	if len(outermost) == 0 {
		return
	}
	sites := make([]site, 0, len(outermost))
	var restores []func()
	for _, call := range outermost {
		parent, child := anchor(parents, call)
		role, index := classify(parent, child)
		sites = append(sites, site{call: call, parent: parent, role: role, index: index})
		leaf := plain(call)
		if parentOfCall := parents[call]; parentOfCall == nil || !replaceExpr(parentOfCall, call, leaf) {
			l.error(call.Pos(), "internal: cannot place ternary")
			continue
		}
		c, p, lf := call, parents[call], leaf
		restores = append(restores, func() { replaceExpr(p, lf, c) })
	}

	l.info = &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	conf := types.Config{
		Importer: importer.Default(),
		Error: func(err error) {
			if strings.Contains(err.Error(), "imported and not used") {
				return
			}
		},
	}
	pkg, _ := conf.Check(l.file.Name.Name, l.fset, []*ast.File{l.file}, l.info)
	l.pkg = pkg

	for _, s := range sites {
		sig := enclosingSig(parents, s.call, l.info)
		t := concrete(contextType(s, parents, l.info, sig))
		if t == nil {
			t = concrete(typeOf(l.info, plain(s.call)))
		}
		if t == nil {
			l.error(s.call.Pos(), "cannot infer type of ternary")
			t = types.Universe.Lookup("any").Type()
		}
		propagate(s.call, t, l.targets)
	}
	for i := len(restores) - 1; i >= 0; i-- {
		restores[i]()
	}
}

func (l *lowerer) error(pos token.Pos, msg string) {
	l.errs = append(l.errs, Error{Pos: l.fset.Position(pos), Msg: msg})
}

func (l *lowerer) walkDecls(decls []ast.Decl, inFunc bool) {
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			old := l.sig
			l.sig = sigOfDecl(l.info, d)
			if d.Body != nil {
				l.lowerBlock(d.Body)
			}
			l.sig = old
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			for _, sp := range d.Specs {
				l.rewriteSpec(sp.(*ast.ValueSpec), inFunc)
			}
		}
	}
}

func (l *lowerer) rewriteSpec(vs *ast.ValueSpec, inFunc bool) {
	if inFunc {
		return
	}
	for i, v := range vs.Values {
		vs.Values[i] = l.forceExpr(v)
	}
}

func (l *lowerer) lowerBlock(b *ast.BlockStmt) {
	if b == nil {
		return
	}
	b.List = l.lowerStmts(b.List)
}

func (l *lowerer) lowerStmts(list []ast.Stmt) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range list {
		out = append(out, l.lowerStmt(s)...)
	}
	return out
}

func (l *lowerer) lowerStmt(stmt ast.Stmt) []ast.Stmt {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		l.lowerBlock(s)
		return []ast.Stmt{s}
	case *ast.AssignStmt:
		return l.lowerAssign(s)
	case *ast.ReturnStmt:
		return l.lowerReturn(s)
	case *ast.DeclStmt:
		return l.lowerDeclStmt(s)
	case *ast.ExprStmt:
		pre, x := l.lowerExpr(s.X)
		s.X = x
		return append(pre, s)
	case *ast.SendStmt:
		var pre []ast.Stmt
		var p []ast.Stmt
		p, s.Chan = l.lowerExpr(s.Chan)
		pre = append(pre, p...)
		p, s.Value = l.lowerExpr(s.Value)
		pre = append(pre, p...)
		return append(pre, s)
	case *ast.IncDecStmt:
		pre, x := l.lowerExpr(s.X)
		s.X = x
		return append(pre, s)
	case *ast.GoStmt:
		return l.lowerCallStmt(s, s.Call)
	case *ast.DeferStmt:
		return l.lowerCallStmt(s, s.Call)
	case *ast.IfStmt:
		return l.lowerIf(s)
	case *ast.ForStmt:
		return l.lowerFor(s)
	case *ast.RangeStmt:
		var pre []ast.Stmt
		var p []ast.Stmt
		p, s.X = l.lowerExpr(s.X)
		pre = append(pre, p...)
		if k, ok := s.Key.(ast.Expr); ok {
			p, s.Key = l.lowerExpr(k)
			pre = append(pre, p...)
		}
		if v, ok := s.Value.(ast.Expr); ok {
			p, s.Value = l.lowerExpr(v)
			pre = append(pre, p...)
		}
		l.lowerBlock(s.Body)
		return append(pre, s)
	case *ast.SwitchStmt:
		pre := l.takeInit(&s.Init)
		var p []ast.Stmt
		p, s.Tag = l.lowerExpr(s.Tag)
		pre = append(pre, p...)
		l.lowerBlock(s.Body)
		return append(pre, s)
	case *ast.TypeSwitchStmt:
		pre := l.takeInit(&s.Init)
		if s.Assign != nil {
			pre = append(pre, l.lowerStmt(s.Assign)...)
			if len(pre) > 0 {
				// assign lowering may have replaced s.Assign if it was the last of pre
			}
		}
		l.lowerBlock(s.Body)
		return append(pre, s)
	case *ast.SelectStmt:
		l.lowerBlock(s.Body)
		return []ast.Stmt{s}
	case *ast.CaseClause:
		for i, e := range s.List {
			s.List[i] = l.forceExpr(e)
		}
		s.Body = l.lowerStmts(s.Body)
		return []ast.Stmt{s}
	case *ast.CommClause:
		if s.Comm != nil {
			comms := l.lowerStmt(s.Comm)
			if len(comms) == 1 {
				s.Comm = comms[0]
			} else if len(comms) > 1 {
				s.Comm = &ast.BlockStmt{List: comms}
			}
		}
		s.Body = l.lowerStmts(s.Body)
		return []ast.Stmt{s}
	case *ast.LabeledStmt:
		ss := l.lowerStmt(s.Stmt)
		if len(ss) == 1 {
			s.Stmt = ss[0]
			return []ast.Stmt{s}
		}
		s.Stmt = &ast.BlockStmt{List: ss}
		return []ast.Stmt{s}
	case *ast.BranchStmt, *ast.EmptyStmt, *ast.BadStmt:
		return []ast.Stmt{s}
	default:
		return []ast.Stmt{stmt}
	}
}

func (l *lowerer) lowerCallStmt(stmt ast.Stmt, call *ast.CallExpr) []ast.Stmt {
	if call == nil {
		return []ast.Stmt{stmt}
	}
	pre := l.rewriteChildExprs(call)
	return append(pre, stmt)
}

func (l *lowerer) lowerIf(s *ast.IfStmt) []ast.Stmt {
	pre := l.takeInit(&s.Init)
	var p []ast.Stmt
	p, s.Cond = l.lowerExpr(s.Cond)
	pre = append(pre, p...)
	l.lowerBlock(s.Body)
	switch e := s.Else.(type) {
	case *ast.BlockStmt:
		l.lowerBlock(e)
	case ast.Stmt:
		ss := l.lowerStmt(e)
		if len(ss) == 1 {
			s.Else = ss[0]
		} else if len(ss) > 1 {
			s.Else = &ast.BlockStmt{List: ss}
		}
	}
	if len(pre) == 0 {
		return []ast.Stmt{s}
	}
	return []ast.Stmt{&ast.BlockStmt{List: append(pre, s)}}
}

func (l *lowerer) lowerFor(s *ast.ForStmt) []ast.Stmt {
	pre := l.takeInit(&s.Init)
	var p []ast.Stmt
	p, s.Cond = l.lowerExpr(s.Cond)
	pre = append(pre, p...)
	if s.Post != nil {
		post := l.lowerStmt(s.Post)
		if len(post) == 1 {
			s.Post = post[0]
		} else if len(post) > 1 {
			pre = append(pre, post[:len(post)-1]...)
			s.Post = post[len(post)-1]
		}
	}
	l.lowerBlock(s.Body)
	if len(pre) == 0 {
		return []ast.Stmt{s}
	}
	return []ast.Stmt{&ast.BlockStmt{List: append(pre, s)}}
}

// takeInit lowers an init statement. A single simple statement stays in
// place; anything larger is returned as statements that must precede the
// parent, and the init slot is cleared.
func (l *lowerer) takeInit(init *ast.Stmt) []ast.Stmt {
	if init == nil || *init == nil {
		return nil
	}
	ss := l.lowerStmt(*init)
	if len(ss) == 1 && simpleStmt(ss[0]) {
		*init = ss[0]
		return nil
	}
	*init = nil
	return ss
}

func simpleStmt(s ast.Stmt) bool {
	switch s.(type) {
	case *ast.AssignStmt, *ast.ExprStmt, *ast.IncDecStmt, *ast.SendStmt, *ast.EmptyStmt:
		return true
	default:
		return false
	}
}

func (l *lowerer) lowerAssign(s *ast.AssignStmt) []ast.Stmt {
	if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
		if call, ok := syntax.Call(s.Rhs[0]); ok && s.Tok == token.DEFINE {
			if id, ok := s.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
				return l.defineTernary(id, call)
			}
		}
		if call, ok := syntax.Call(s.Rhs[0]); ok && s.Tok == token.ASSIGN {
			if _, ok := s.Lhs[0].(*ast.Ident); ok {
				return l.assignIdent(s.Lhs[0].(*ast.Ident), call)
			}
		}
	}
	var pre []ast.Stmt
	for i := range s.Rhs {
		var p []ast.Stmt
		p, s.Rhs[i] = l.lowerExpr(s.Rhs[i])
		pre = append(pre, p...)
	}
	for i := range s.Lhs {
		var p []ast.Stmt
		p, s.Lhs[i] = l.lowerExpr(s.Lhs[i])
		pre = append(pre, p...)
	}
	return append(pre, s)
}

func (l *lowerer) lowerReturn(s *ast.ReturnStmt) []ast.Stmt {
	if len(s.Results) == 1 {
		if call, ok := syntax.Call(s.Results[0]); ok {
			return l.returnTernary(call)
		}
	}
	var pre []ast.Stmt
	for i := range s.Results {
		var p []ast.Stmt
		p, s.Results[i] = l.lowerExpr(s.Results[i])
		pre = append(pre, p...)
	}
	return append(pre, s)
}

func (l *lowerer) lowerDeclStmt(s *ast.DeclStmt) []ast.Stmt {
	gd, ok := s.Decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.VAR {
		return []ast.Stmt{s}
	}
	var out []ast.Stmt
	for _, sp := range gd.Specs {
		vs := sp.(*ast.ValueSpec)
		out = append(out, l.lowerLocalSpec(vs)...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (l *lowerer) lowerLocalSpec(vs *ast.ValueSpec) []ast.Stmt {
	if len(vs.Names) == 1 && len(vs.Values) == 1 {
		if call, ok := syntax.Call(vs.Values[0]); ok {
			return l.defineTernaryTyped(vs.Names[0], vs.Type, call)
		}
	}
	var pre []ast.Stmt
	vals := append([]ast.Expr(nil), vs.Values...)
	for i, v := range vals {
		var p []ast.Stmt
		p, vals[i] = l.lowerExpr(v)
		pre = append(pre, p...)
	}
	vs.Values = vals
	decl := &ast.DeclStmt{Decl: &ast.GenDecl{TokPos: vs.Pos(), Tok: token.VAR, Specs: []ast.Spec{vs}}}
	return append(pre, decl)
}

func (l *lowerer) defineTernary(id *ast.Ident, call *ast.CallExpr) []ast.Stmt {
	return l.defineTernaryTyped(id, nil, call)
}

func (l *lowerer) defineTernaryTyped(id *ast.Ident, explicit ast.Expr, call *ast.CallExpr) []ast.Stmt {
	pos := call.Lparen
	if !pos.IsValid() {
		pos = call.Pos()
	}
	var typ ast.Expr
	if explicit != nil {
		typ = explicit
	} else {
		typ = l.typeAST(l.targets[call], pos)
	}
	decl := &ast.DeclStmt{Decl: &ast.GenDecl{
		TokPos: pos,
		Tok:    token.VAR,
		Specs: []ast.Spec{&ast.ValueSpec{
			Names: []*ast.Ident{id},
			Type:  typ,
		}},
	}}
	return append([]ast.Stmt{decl}, l.ifAssign(id.Name, pos, call)...)
}

func (l *lowerer) assignIdent(id *ast.Ident, call *ast.CallExpr) []ast.Stmt {
	pos := call.Pos()
	return l.ifAssign(id.Name, pos, call)
}

func (l *lowerer) returnTernary(call *ast.CallExpr) []ast.Stmt {
	pos := call.Pos()
	condPre, cond := l.lowerExpr(call.Args[1])
	thenPre, thenE := l.lowerExpr(call.Args[0])
	elsePre, elseE := l.lowerExpr(call.Args[2])
	iff := &ast.IfStmt{
		If:   pos,
		Cond: cond,
		Body: block(pos, append(thenPre, &ast.ReturnStmt{Return: pos, Results: []ast.Expr{thenE}})),
		Else: block(pos, append(elsePre, &ast.ReturnStmt{Return: pos, Results: []ast.Expr{elseE}})),
	}
	return append(condPre, iff)
}

func (l *lowerer) ifAssign(name string, pos token.Pos, call *ast.CallExpr) []ast.Stmt {
	condPre, cond := l.lowerExpr(call.Args[1])
	thenPre, thenE := l.lowerExpr(call.Args[0])
	elsePre, elseE := l.lowerExpr(call.Args[2])
	iff := &ast.IfStmt{
		If:   pos,
		Cond: cond,
		Body: block(pos, append(thenPre, assignName(name, pos, thenE))),
		Else: block(pos, append(elsePre, assignName(name, pos, elseE))),
	}
	return append(condPre, iff)
}

func (l *lowerer) lowerExpr(e ast.Expr) ([]ast.Stmt, ast.Expr) {
	if e == nil {
		return nil, nil
	}
	switch e.(type) {
	case *ast.Ident, *ast.BasicLit, *ast.BadExpr:
		return nil, e
	}
	if _, ok := syntax.Call(e); ok {
		return l.lowerTernary(e)
	}
	if fl, ok := e.(*ast.FuncLit); ok {
		old := l.sig
		if l.info != nil {
			if tv, ok := l.info.Types[fl]; ok {
				if sig, ok := tv.Type.(*types.Signature); ok {
					l.sig = sig
				}
			}
		}
		l.lowerBlock(fl.Body)
		l.sig = old
	}
	pre := l.rewriteChildExprs(e)
	return pre, e
}

func (l *lowerer) lowerTernary(e ast.Expr) ([]ast.Stmt, ast.Expr) {
	call, _ := syntax.Call(e)
	pos := call.Pos()
	name := l.fresh()
	t := l.targets[call]
	if t == nil {
		t = types.Universe.Lookup("any").Type()
	}
	if l.idents == nil {
		l.idents = map[string]types.Type{}
	}
	l.idents[name] = t
	condPre, cond := l.lowerExpr(call.Args[1])
	thenPre, thenE := l.lowerExpr(call.Args[0])
	elsePre, elseE := l.lowerExpr(call.Args[2])
	decl := varName(name, l.typeAST(t, pos), pos)
	iff := &ast.IfStmt{
		If:   pos,
		Cond: cond,
		Body: block(pos, append(thenPre, assignName(name, pos, thenE))),
		Else: block(pos, append(elsePre, assignName(name, pos, elseE))),
	}
	pre := append(condPre, decl, iff)
	return pre, &ast.Ident{NamePos: pos, Name: name}
}

// forceExpr lowers e and, if that needs statements, wraps them in an
// immediately called function. Used where a statement would be illegal
// (package scope, case labels).
func (l *lowerer) forceExpr(e ast.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	pre, out := l.lowerExpr(e)
	if len(pre) == 0 {
		return out
	}
	pos := e.Pos()
	t := l.typeOfExpr(out, e)
	return iife(pre, out, l.typeAST(t, pos), pos)
}

func (l *lowerer) typeOfExpr(out, orig ast.Expr) types.Type {
	if id, ok := out.(*ast.Ident); ok && l.idents != nil {
		if t := l.idents[id.Name]; t != nil {
			return t
		}
	}
	if call, ok := syntax.Call(orig); ok {
		if t := l.targets[call]; t != nil {
			return t
		}
	}
	return types.Universe.Lookup("any").Type()
}

func (l *lowerer) typeAST(t types.Type, pos token.Pos) ast.Expr {
	if t == nil {
		t = types.Universe.Lookup("any").Type()
	}
	s := types.TypeString(t, func(p *types.Package) string {
		if p == nil || p == l.pkg {
			return ""
		}
		return p.Name()
	})
	e, err := parser.ParseExpr(s)
	if err != nil {
		e = &ast.Ident{Name: "any"}
	}
	shiftPos(e, pos)
	return e
}

func (l *lowerer) fresh() string {
	l.tmp++
	return fmt.Sprintf("__bgo%d", l.tmp)
}

func (l *lowerer) rewriteChildExprs(e ast.Expr) []ast.Stmt {
	if e == nil {
		return nil
	}
	var pre []ast.Stmt
	rewriteValue(reflect.ValueOf(e), func(x ast.Expr) ([]ast.Stmt, ast.Expr) {
		return l.lowerExpr(x)
	}, &pre)
	return pre
}

func varName(name string, typ ast.Expr, pos token.Pos) ast.Stmt {
	return &ast.DeclStmt{Decl: &ast.GenDecl{
		TokPos: pos,
		Tok:    token.VAR,
		Specs: []ast.Spec{&ast.ValueSpec{
			Names: []*ast.Ident{{NamePos: pos, Name: name}},
			Type:  typ,
		}},
	}}
}

func assignName(name string, pos token.Pos, rhs ast.Expr) ast.Stmt {
	return &ast.AssignStmt{
		Lhs:    []ast.Expr{&ast.Ident{NamePos: pos, Name: name}},
		TokPos: pos,
		Tok:    token.ASSIGN,
		Rhs:    []ast.Expr{rhs},
	}
}

func block(pos token.Pos, list []ast.Stmt) *ast.BlockStmt {
	return &ast.BlockStmt{Lbrace: pos, List: list, Rbrace: pos}
}

func iife(pre []ast.Stmt, result, typ ast.Expr, pos token.Pos) ast.Expr {
	body := append(append([]ast.Stmt{}, pre...), &ast.ReturnStmt{Return: pos, Results: []ast.Expr{result}})
	lit := &ast.FuncLit{
		Type: &ast.FuncType{
			Func:   pos,
			Params: &ast.FieldList{Opening: pos, Closing: pos},
			Results: &ast.FieldList{
				Opening: pos,
				Closing: pos,
				List:    []*ast.Field{{Type: typ}},
			},
		},
		Body: &ast.BlockStmt{Lbrace: pos, List: body, Rbrace: pos},
	}
	return &ast.CallExpr{Fun: lit, Lparen: pos, Rparen: pos}
}

func shiftPos(e ast.Expr, pos token.Pos) {
	// Best-effort: give synthetic type syntax a real position so the printer
	// can attach a //line directive. Identifiers are the common case.
	if id, ok := e.(*ast.Ident); ok && !id.NamePos.IsValid() {
		id.NamePos = pos
	}
}

func plain(e ast.Expr) ast.Expr {
	for {
		if call, ok := syntax.Call(e); ok {
			e = call.Args[0]
			continue
		}
		return e
	}
}

func isParenOf(e ast.Expr, call *ast.CallExpr) bool {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return false
		}
		if p.X == call {
			return true
		}
		e = p.X
	}
}

func parentMap(file *ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	return parents
}

func nestedInTernary(parents map[ast.Node]ast.Node, n ast.Node) bool {
	for p := parents[n]; p != nil; p = parents[p] {
		call, ok := p.(*ast.CallExpr)
		if ok {
			if _, ok := syntax.Call(call); ok {
				return true
			}
		}
	}
	return false
}

func anchor(parents map[ast.Node]ast.Node, n ast.Node) (parent ast.Node, child ast.Expr) {
	cur := n
	for {
		p := parents[cur]
		if p == nil {
			e, _ := cur.(ast.Expr)
			return nil, e
		}
		switch p.(type) {
		case *ast.ParenExpr, *ast.BinaryExpr, *ast.UnaryExpr:
			cur = p
			continue
		default:
			e, _ := cur.(ast.Expr)
			return p, e
		}
	}
}

func enclosingSig(parents map[ast.Node]ast.Node, n ast.Node, info *types.Info) *types.Signature {
	if info == nil {
		return nil
	}
	for p := parents[n]; p != nil; p = parents[p] {
		switch f := p.(type) {
		case *ast.FuncDecl:
			if f.Name != nil {
				if obj := info.Defs[f.Name]; obj != nil {
					if sig, ok := obj.Type().(*types.Signature); ok {
						return sig
					}
				}
			}
		case *ast.FuncLit:
			if tv, ok := info.Types[f]; ok {
				if sig, ok := tv.Type.(*types.Signature); ok {
					return sig
				}
			}
		}
	}
	return nil
}

func sigOfDecl(info *types.Info, d *ast.FuncDecl) *types.Signature {
	if info == nil || d.Name == nil {
		return nil
	}
	if obj := info.Defs[d.Name]; obj != nil {
		if sig, ok := obj.Type().(*types.Signature); ok {
			return sig
		}
	}
	return nil
}

type site struct {
	call   *ast.CallExpr
	parent ast.Node
	role   string
	index  int
}

func classify(parent ast.Node, child ast.Expr) (string, int) {
	switch p := parent.(type) {
	case *ast.IfStmt:
		if p.Cond == child {
			return "bool", 0
		}
	case *ast.ForStmt:
		if p.Cond == child {
			return "bool", 0
		}
	case *ast.ReturnStmt:
		return "return", indexOf(p.Results, child)
	case *ast.AssignStmt:
		return "assign", indexOf(p.Rhs, child)
	case *ast.ValueSpec:
		return "value", indexOf(p.Values, child)
	case *ast.CallExpr:
		return "arg", indexOf(p.Args, child)
	case *ast.SendStmt:
		if p.Value == child {
			return "send", 0
		}
	case *ast.CompositeLit:
		return "elem", 0
	case *ast.IndexExpr:
		if p.Index == child {
			return "index", 0
		}
	case *ast.KeyValueExpr:
		if p.Value == child {
			return "kvvalue", 0
		}
		if p.Key == child {
			return "kvkey", 0
		}
	case *ast.SwitchStmt:
		if p.Tag == child {
			return "tag", 0
		}
	}
	return "", -1
}

func contextType(s site, parents map[ast.Node]ast.Node, info *types.Info, sig *types.Signature) types.Type {
	if info == nil {
		return nil
	}
	switch s.role {
	case "bool":
		return types.Typ[types.Bool]
	case "return":
		if sig != nil && s.index >= 0 && s.index < sig.Results().Len() {
			return sig.Results().At(s.index).Type()
		}
	case "assign":
		as, ok := s.parent.(*ast.AssignStmt)
		if !ok || s.index < 0 || s.index >= len(as.Lhs) {
			return nil
		}
		if as.Tok == token.DEFINE {
			if id, ok := as.Lhs[s.index].(*ast.Ident); ok && id.Name != "_" {
				if obj := info.Defs[id]; obj != nil {
					return obj.Type()
				}
			}
		}
		return typeOf(info, as.Lhs[s.index])
	case "value":
		vs, ok := s.parent.(*ast.ValueSpec)
		if !ok {
			return nil
		}
		if vs.Type != nil {
			if t := typeOf(info, vs.Type); t != nil {
				return t
			}
		}
		if s.index >= 0 && s.index < len(vs.Names) {
			if obj := info.Defs[vs.Names[s.index]]; obj != nil {
				return obj.Type()
			}
		}
	case "arg":
		call, ok := s.parent.(*ast.CallExpr)
		if !ok {
			return nil
		}
		tv, ok := info.Types[call.Fun]
		if !ok || tv.Type == nil {
			return nil
		}
		csig, ok := tv.Type.Underlying().(*types.Signature)
		if !ok {
			return nil
		}
		params := csig.Params()
		if params.Len() == 0 || s.index < 0 {
			return nil
		}
		if csig.Variadic() && s.index >= params.Len()-1 {
			if sl, ok := params.At(params.Len() - 1).Type().(*types.Slice); ok {
				return sl.Elem()
			}
		}
		if s.index < params.Len() {
			return params.At(s.index).Type()
		}
	case "send":
		ss, ok := s.parent.(*ast.SendStmt)
		if !ok {
			return nil
		}
		if tv, ok := info.Types[ss.Chan]; ok && tv.Type != nil {
			if ch, ok := tv.Type.Underlying().(*types.Chan); ok {
				return ch.Elem()
			}
		}
	case "elem":
		cl, ok := s.parent.(*ast.CompositeLit)
		if ok && cl.Type != nil {
			return elemType(typeOf(info, cl.Type))
		}
	case "index":
		ix, ok := s.parent.(*ast.IndexExpr)
		if !ok {
			return nil
		}
		xt := typeOf(info, ix.X)
		if xt == nil {
			return nil
		}
		switch t := xt.Underlying().(type) {
		case *types.Map:
			return t.Key()
		case *types.Slice, *types.Array:
			return types.Typ[types.Int]
		}
	case "kvvalue", "kvkey":
		comp, _ := parents[s.parent].(*ast.CompositeLit)
		if comp == nil || comp.Type == nil {
			return nil
		}
		mt := typeOf(info, comp.Type)
		if mt == nil {
			return nil
		}
		if s.role == "kvkey" {
			if m, ok := mt.Underlying().(*types.Map); ok {
				return m.Key()
			}
			return nil
		}
		return elemType(mt)
	case "tag":
		sw, ok := s.parent.(*ast.SwitchStmt)
		if ok {
			return typeOf(info, sw.Tag)
		}
	}
	return nil
}

func typeOf(info *types.Info, e ast.Expr) types.Type {
	if info == nil || e == nil {
		return nil
	}
	if tv, ok := info.Types[e]; ok {
		return tv.Type
	}
	return nil
}

func elemType(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	switch t := t.Underlying().(type) {
	case *types.Slice:
		return t.Elem()
	case *types.Array:
		return t.Elem()
	case *types.Map:
		return t.Elem()
	default:
		return nil
	}
}

func concrete(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok || b.Info()&types.IsUntyped == 0 {
		return t
	}
	if b.Kind() == types.UntypedNil {
		return nil
	}
	return types.Default(t)
}

func indexOf(list []ast.Expr, e ast.Expr) int {
	for i, x := range list {
		if x == e {
			return i
		}
	}
	return -1
}

func propagate(call *ast.CallExpr, t types.Type, dst map[*ast.CallExpr]types.Type) {
	if call == nil || t == nil {
		return
	}
	dst[call] = t
	if c, ok := syntax.Call(call.Args[0]); ok {
		propagate(c, t, dst)
	}
	if c, ok := syntax.Call(call.Args[2]); ok {
		propagate(c, t, dst)
	}
	if c, ok := syntax.Call(call.Args[1]); ok {
		propagate(c, types.Typ[types.Bool], dst)
	}
}

func replaceExpr(parent ast.Node, old, new ast.Expr) bool {
	if parent == nil {
		return false
	}
	v := reflect.ValueOf(parent)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return false
	}
	return replaceIn(v.Elem(), old, new)
}

func replaceIn(v reflect.Value, old, new ast.Expr) bool {
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return false
		}
		if v.Elem().Kind() == reflect.Struct && v.Elem().Type().PkgPath() == "go/ast" {
			return replaceIn(v.Elem(), old, new)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if replaceIn(v.Field(i), old, new) {
				return true
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if replaceIn(v.Index(i), old, new) {
				return true
			}
		}
	case reflect.Interface:
		if v.IsNil() || v.Type() != exprIface || !v.CanSet() {
			return false
		}
		if v.Interface().(ast.Expr) == old {
			v.Set(reflect.ValueOf(new))
			return true
		}
	}
	return false
}

func rewriteValue(v reflect.Value, fn func(ast.Expr) ([]ast.Stmt, ast.Expr), pre *[]ast.Stmt) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		elem := v.Type().Elem()
		if elem.PkgPath() == "go/ast" && (elem.Name() == "Object" || elem.Name() == "Scope") {
			return
		}
		rewriteValue(v.Elem(), fn, pre)
	case reflect.Struct:
		t := v.Type()
		if t.PkgPath() != "" && t.PkgPath() != "go/ast" {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				continue
			}
			rewriteValue(v.Field(i), fn, pre)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			rewriteValue(v.Index(i), fn, pre)
		}
	case reflect.Interface:
		if v.IsNil() || v.Type() != exprIface || !v.CanSet() {
			return
		}
		e := v.Interface().(ast.Expr)
		p, out := fn(e)
		*pre = append(*pre, p...)
		if out != nil {
			v.Set(reflect.ValueOf(out))
		}
	}
}
