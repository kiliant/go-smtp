package smtpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAPISurfaceRules is the nested-module counterpart of the root module's
// API gate. A Go workspace does not make `go test ./...` cross module
// boundaries, so smtpserver must enforce the same invariants inside its own
// test run rather than assuming the root scanner sees it.
func TestAPISurfaceRules(t *testing.T) {
	for _, directory := range []string{".", "memory", "backendtest"} {
		checkPackageSurface(t, directory)
	}
}

func checkPackageSurface(t *testing.T, directory string) {
	t.Helper()
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, directory, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range packages {
		for filename, file := range pkg.Files {
			imports := importPaths(file)
			for _, declaration := range file.Decls {
				switch declaration := declaration.(type) {
				case *ast.GenDecl:
					checkTypeDeclarations(t, set, filename, declaration, imports)
				case *ast.FuncDecl:
					checkFunctionDeclaration(t, set, filename, declaration, imports)
				}
			}
		}
	}
}

func checkTypeDeclarations(t *testing.T, set *token.FileSet, filename string, declaration *ast.GenDecl, imports map[string]string) {
	if declaration.Tok != token.TYPE {
		return
	}
	for _, spec := range declaration.Specs {
		typeSpec := spec.(*ast.TypeSpec)
		if !typeSpec.Name.IsExported() {
			continue
		}
		if declaration.Doc == nil && typeSpec.Doc == nil {
			t.Errorf("%s: exported type %s has no doc comment", position(set, filename, typeSpec.Pos()), typeSpec.Name.Name)
		}
		if _, ok := typeSpec.Type.(*ast.InterfaceType); ok {
			t.Errorf("%s: exported interface %s is forbidden", position(set, filename, typeSpec.Pos()), typeSpec.Name.Name)
		}
		if exposesInternal(typeSpec.Type, imports) {
			t.Errorf("%s: exported type %s exposes an internal package", position(set, filename, typeSpec.Pos()), typeSpec.Name.Name)
		}
		structure, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			continue
		}
		if externallyConstructible(structure) && !hasGuard(structure) {
			t.Errorf("%s: exported constructible struct %s lacks an unexported guard", position(set, filename, typeSpec.Pos()), typeSpec.Name.Name)
		}
		for _, field := range structure.Fields.List {
			if len(field.Names) == 0 || !field.Names[0].IsExported() {
				continue
			}
			function, ok := field.Type.(*ast.FuncType)
			if !ok || typeSpec.Name.Name != "Backend" && typeSpec.Name.Name != "Session" {
				continue
			}
			if !contextFirst(function) {
				t.Errorf("%s: handler field %s.%s is not context-first", position(set, filename, field.Pos()), typeSpec.Name.Name, field.Names[0].Name)
			}
			if !optionsLast(function) {
				t.Errorf("%s: handler field %s.%s has no trailing *Options", position(set, filename, field.Pos()), typeSpec.Name.Name, field.Names[0].Name)
			}
		}
	}
}

func checkFunctionDeclaration(t *testing.T, set *token.FileSet, filename string, declaration *ast.FuncDecl, imports map[string]string) {
	if !declaration.Name.IsExported() || !exportedReceiver(declaration.Recv) {
		return
	}
	if declaration.Doc == nil {
		t.Errorf("%s: exported function %s has no doc comment", position(set, filename, declaration.Pos()), declaration.Name.Name)
	}
	if exposesInternal(declaration.Type, imports) {
		t.Errorf("%s: exported function %s exposes an internal package", position(set, filename, declaration.Pos()), declaration.Name.Name)
	}
	switch declaration.Name.Name {
	case "Shutdown", "Run":
		if !contextFirst(declaration.Type) {
			t.Errorf("%s: %s is not context-first", position(set, filename, declaration.Pos()), declaration.Name.Name)
		}
		if !optionsLast(declaration.Type) {
			t.Errorf("%s: %s has no trailing *Options", position(set, filename, declaration.Pos()), declaration.Name.Name)
		}
	case "New", "NewServer":
		if !optionsLast(declaration.Type) {
			t.Errorf("%s: %s has no trailing *Options", position(set, filename, declaration.Pos()), declaration.Name.Name)
		}
	}
}

func exportedReceiver(receiver *ast.FieldList) bool {
	if receiver == nil {
		return true
	}
	if len(receiver.List) != 1 {
		return false
	}
	typeName := receiver.List[0].Type
	if pointer, ok := typeName.(*ast.StarExpr); ok {
		typeName = pointer.X
	}
	identifier, ok := typeName.(*ast.Ident)
	return ok && identifier.IsExported()
}

func importPaths(file *ast.File) map[string]string {
	paths := make(map[string]string)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		paths[name] = path
	}
	return paths
}

func exposesInternal(node ast.Node, imports map[string]string) bool {
	exposed := false
	ast.Inspect(node, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && strings.Contains(imports[identifier.Name], "/internal/") {
			exposed = true
			return false
		}
		return true
	})
	return exposed
}

func externallyConstructible(structure *ast.StructType) bool {
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if !name.IsExported() {
				return false
			}
		}
	}
	return true
}

func hasGuard(structure *ast.StructType) bool {
	for _, field := range structure.Fields.List {
		if len(field.Names) != 1 || field.Names[0].Name != "_" {
			continue
		}
		if nested, ok := field.Type.(*ast.StructType); ok && len(nested.Fields.List) == 0 {
			return true
		}
	}
	return false
}

func contextFirst(function *ast.FuncType) bool {
	if function.Params == nil || len(function.Params.List) == 0 {
		return false
	}
	selector, ok := function.Params.List[0].Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "context"
}

func optionsLast(function *ast.FuncType) bool {
	if function.Params == nil || len(function.Params.List) == 0 {
		return false
	}
	pointer, ok := function.Params.List[len(function.Params.List)-1].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	identifier, ok := pointer.X.(*ast.Ident)
	return ok && strings.HasSuffix(identifier.Name, "Options")
}

func position(set *token.FileSet, filename string, pos token.Pos) string {
	position := set.Position(pos)
	if position.Filename == "" {
		position.Filename = filename
	}
	return position.String()
}
