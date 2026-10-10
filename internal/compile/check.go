package compile

import (
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"strings"

	"bettergo/internal/lower"
)

// Check typechecks lowered Go. Positions still refer to the .bgo file.
func Check(res *Result) (*types.Info, *types.Package, []lower.Error) {
	if res == nil || res.File == nil {
		return nil, nil, nil
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	var diags []lower.Error
	conf := types.Config{
		Importer: importer.Default(),
		Error: func(err error) {
			if te, ok := err.(types.Error); ok {
				diags = append(diags, lower.Error{Pos: te.Fset.Position(te.Pos), Msg: te.Msg})
				return
			}
			diags = append(diags, lower.Error{Pos: token.Position{}, Msg: err.Error()})
		},
	}
	pkg, _ := conf.Check(res.File.Name.Name, res.Fset, []*ast.File{res.File}, info)
	// Drop complaints about the synthetic helpers; the user-facing positions remain.
	out := diags[:0]
	for _, d := range diags {
		if strings.Contains(d.Msg, "__bgo") && strings.Contains(d.Msg, "imported and not used") {
			continue
		}
		out = append(out, d)
	}
	return info, pkg, out
}
