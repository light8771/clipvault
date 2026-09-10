package clipvault

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// Check literal keys in Go as well as templates: otherwise a missing backend
// error translation only surfaces when a user takes that error path.
func TestI18nGoKeysAreDefined(t *testing.T) {
	defined := make(map[string]bool, len(BuiltinTranslations))
	for _, entry := range BuiltinTranslations {
		defined[entry.Key] = true
	}
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				var name string
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					name = fun.Sel.Name
				}
				if name != "templateT" && name != "i18nT" && name != "T" && name != "t" {
					return true
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				key, err := strconv.Unquote(literal.Value)
				if err != nil || !defined[key] {
					t.Errorf("%s references undefined i18n key %q", fset.Position(literal.Pos()), key)
				}
				return true
			})
		}
	}
}
