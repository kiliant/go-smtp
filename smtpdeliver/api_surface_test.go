package smtpdeliver

// api_surface_test.go — smtpdeliver's own mechanical API gates (T25). The
// module-wide rules (no internal/ leak, keyed-literal guards, no exported
// interfaces, RFC-cited docs) run from the root api_surface_test.go, which
// includes this package. The gates here are the ones specific to this
// package's shape (docs/DELIVERY-DESIGN.md §§2, 3, 5, 8).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// openStringTypes are the open-ended sets of DELIVERY-DESIGN.md: each must
// stay a string-backed type so unknown values round-trip (API-STABILITY.md
// §1).
var openStringTypes = []string{
	"AttemptStage", "DANEMode", "DNSSECStatus", "Disposition", "EventKind",
	"LookupState", "MTASTSMode", "PolicyKind", "PolicySource",
}

// callbackStructs are the extension points that must be structs of function
// fields, never interfaces (DELIVERY-DESIGN.md §10).
var callbackStructs = []string{"Resolver", "PolicyCache"}

// nonBlockingDelivererMethods would list exported Deliverer methods that do
// no I/O. Empty: every method so far blocks. Adding one is an API decision.
var nonBlockingDelivererMethods = map[string]bool{}

const modulePath = "github.com/kiliant/go-smtp"

// allowedModuleImports are the only packages of this module smtpdeliver may
// import (DELIVERY-DESIGN.md §1). internal/ is deliberately absent.
var allowedModuleImports = map[string]bool{
	modulePath:                 true,
	modulePath + "/smtpclient": true,
}

func loadOwnPackage(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatal("no smtpdeliver sources found")
	}
	return fset, files
}

func parseSource(t *testing.T, src string) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return fset, map[string]*ast.File{"synthetic.go": f}
}

// delivererMethodViolations enforces API-STABILITY.md §§2–3 on Deliverer:
// context first, an options pointer last.
func delivererMethodViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || receiverName(fn) != "Deliverer" || nonBlockingDelivererMethods[fn.Name.Name] {
				continue
			}
			params := flattenParams(fn.Type.Params)
			pos := fset.Position(fn.Pos())
			if len(params) == 0 || !isSelector(params[0], "context", "Context") {
				out = append(out, fmt.Sprintf("%s: Deliverer.%s must take context.Context first (API-STABILITY.md §2)", pos, fn.Name.Name))
			}
			if len(params) < 2 || !isOptionsPointer(params[len(params)-1]) {
				out = append(out, fmt.Sprintf("%s: Deliverer.%s must take a *...Options parameter last (API-STABILITY.md §3)", pos, fn.Name.Name))
			}
		}
	}
	sort.Strings(out)
	return out
}

// constructorViolations allows exactly one exported function, New(*Options),
// which does no I/O.
func constructorViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			params := flattenParams(fn.Type.Params)
			if fn.Name.Name != "New" || len(params) != 1 || !isOptionsPointer(params[0]) {
				out = append(out, fmt.Sprintf("%s: exported function %s: smtpdeliver exports only New(*Options); new entry points are Deliverer methods with ctx and options (API-STABILITY.md §§2–3)", fset.Position(fn.Pos()), fn.Name.Name))
			}
		}
	}
	return out
}

func openStringViolations(files map[string]*ast.File, names []string) []string {
	var out []string
	for _, name := range names {
		ts := findType(files, name)
		if ts == nil {
			out = append(out, fmt.Sprintf("%s: open string type is missing", name))
			continue
		}
		if id, ok := ts.Type.(*ast.Ident); !ok || id.Name != "string" || ts.Assign.IsValid() {
			out = append(out, fmt.Sprintf("%s must be a defined string type so unknown values round-trip (API-STABILITY.md §1)", name))
		}
	}
	return out
}

func callbackStructViolations(files map[string]*ast.File, names []string) []string {
	var out []string
	for _, name := range names {
		ts := findType(files, name)
		if ts == nil {
			out = append(out, fmt.Sprintf("%s: callback struct is missing", name))
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			out = append(out, fmt.Sprintf("%s must be a struct of function fields, not %T (DELIVERY-DESIGN.md §10)", name, ts.Type))
			continue
		}
		for _, field := range st.Fields.List {
			for _, fieldName := range field.Names {
				if !fieldName.IsExported() {
					continue
				}
				if _, ok := field.Type.(*ast.FuncType); !ok {
					out = append(out, fmt.Sprintf("%s.%s must be a function field (DELIVERY-DESIGN.md §10)", name, fieldName.Name))
				}
			}
		}
	}
	return out
}

func importViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			first := strings.SplitN(path, "/", 2)[0]
			if strings.HasPrefix(path, modulePath) {
				if !allowedModuleImports[path] {
					out = append(out, fmt.Sprintf("%s: smtpdeliver must not import %s (DELIVERY-DESIGN.md §1)", fset.Position(imp.Pos()), path))
				}
				continue
			}
			if strings.Contains(first, ".") {
				out = append(out, fmt.Sprintf("%s: smtpdeliver imports %s; only the standard library is allowed (zero dependencies)", fset.Position(imp.Pos()), path))
			}
		}
	}
	return out
}

func TestAPISurfaceDelivererMethods(t *testing.T) {
	fset, files := loadOwnPackage(t)
	for _, v := range delivererMethodViolations(fset, files) {
		t.Error(v)
	}
	for _, v := range constructorViolations(fset, files) {
		t.Error(v)
	}
}

func TestAPISurfaceOpenStringTypes(t *testing.T) {
	_, files := loadOwnPackage(t)
	for _, v := range openStringViolations(files, openStringTypes) {
		t.Error(v)
	}
}

func TestAPISurfaceCallbackStructs(t *testing.T) {
	_, files := loadOwnPackage(t)
	for _, v := range callbackStructViolations(files, callbackStructs) {
		t.Error(v)
	}
}

func TestAPISurfaceImports(t *testing.T) {
	fset, files := loadOwnPackage(t)
	for _, v := range importViolations(fset, files) {
		t.Error(v)
	}
}

// TestAPISurfaceGateLogic proves every gate above catches a shape the real
// code could plausibly drift into, and passes the shape it is meant to allow.
func TestAPISurfaceGateLogic(t *testing.T) {
	const src = `package smtpdeliver

import (
	"context"
	"net"

	"github.com/kiliant/go-smtp/internal/smtpwire"
	"github.com/kiliant/go-smtp/smtpclient"
	"example.com/dns"
)

type Deliverer struct{}
type Options struct{}
type DeliverOptions struct{}

// Correct shapes.
func New(opts *Options) (*Deliverer, error) { return nil, nil }
func (d *Deliverer) Deliver(ctx context.Context, req *Request, opts *DeliverOptions) error { return nil }

// Drifted shapes.
func (d *Deliverer) Probe(domain string, opts *DeliverOptions) error { return nil }
func (d *Deliverer) Flush(ctx context.Context) error { return nil }
func (d *Deliverer) Lookup(ctx context.Context, opts *DeliverOptions, name string) error { return nil }
func NewWithResolver(r Resolver) *Deliverer { return nil }
func Deliver(ctx context.Context, req *Request) error { return nil }

type Disposition int
type DANEMode = string
type PolicyKind string

type Resolver struct {
	LookupMX func(context.Context, string) error
	Backend  net.Resolver
}
type PolicyCache interface{ Load() }

var _ smtpwire.Reply
var _ smtpclient.Client
`
	fset, files := parseSource(t, src)

	methods := delivererMethodViolations(fset, files)
	wantMethods := []string{"Deliverer.Flush must take a *...Options", "Deliverer.Lookup must take a *...Options", "Deliverer.Probe must take context.Context first"}
	requireViolations(t, "methods", methods, wantMethods, []string{"Deliverer.Deliver"})

	ctors := constructorViolations(fset, files)
	requireViolations(t, "constructors", ctors, []string{"function NewWithResolver", "function Deliver:"}, []string{"function New:"})

	strs := openStringViolations(files, []string{"Disposition", "DANEMode", "PolicyKind", "Missing"})
	requireViolations(t, "open strings", strs, []string{"Disposition must be", "DANEMode must be", "Missing: open string type is missing"}, []string{"PolicyKind"})

	cbs := callbackStructViolations(files, []string{"Resolver", "PolicyCache"})
	requireViolations(t, "callback structs", cbs, []string{"Resolver.Backend must be a function field", "PolicyCache must be a struct"}, []string{"Resolver.LookupMX"})

	imps := importViolations(fset, files)
	requireViolations(t, "imports", imps, []string{"must not import github.com/kiliant/go-smtp/internal/smtpwire", "imports example.com/dns"}, []string{"smtpclient", "\"context\"", "\"net\""})
}

func requireViolations(t *testing.T, gate string, got, want, absent []string) {
	t.Helper()
	joined := strings.Join(got, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("%s gate missed %q; got:\n%s", gate, w, joined)
		}
	}
	for _, a := range absent {
		for _, line := range got {
			if strings.Contains(line, a) {
				t.Errorf("%s gate wrongly flagged %q: %s", gate, a, line)
			}
		}
	}
}

func receiverName(fn *ast.FuncDecl) string {
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func flattenParams(list *ast.FieldList) []ast.Expr {
	var out []ast.Expr
	if list == nil {
		return out
	}
	for _, field := range list.List {
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for range n {
			out = append(out, field.Type)
		}
	}
	return out
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

func isOptionsPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch x := star.X.(type) {
	case *ast.Ident:
		return strings.HasSuffix(x.Name, "Options")
	case *ast.SelectorExpr:
		return strings.HasSuffix(x.Sel.Name, "Options")
	}
	return false
}

func findType(files map[string]*ast.File, name string) *ast.TypeSpec {
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
					return ts
				}
			}
		}
	}
	return nil
}
