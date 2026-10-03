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

// openStringTypes are the open-ended sets of DELIVERY-DESIGN.md. Each must
// exist and stay a string-backed type so unknown values round-trip
// (API-STABILITY.md §1). Any other exported type with exported constants is
// held to the same rule automatically by enumViolations.
var openStringTypes = []string{
	"AttemptStage", "DANEMode", "DNSSECStatus", "Disposition", "EventKind",
	"LookupState", "MTASTSMode", "PolicyKind", "PolicySource",
}

// callbackStructs are the extension points that must be structs of function
// fields, never interfaces (DELIVERY-DESIGN.md §10).
var callbackStructs = []string{"Resolver", "PolicyCache"}

// nonBlockingMethods lists exported methods, as "Type.Method", that do no I/O
// and are therefore exempt from the context-first and options gates. Empty:
// every method so far blocks. Adding one is an API decision.
var nonBlockingMethods = map[string]bool{}

// exportedFunctions are the only exported package-level functions. Each takes
// a trailing *...Options of this package. Adding one is an API decision; most
// new behaviour belongs on Deliverer or in an options field.
var exportedFunctions = map[string]bool{
	"New":                  true,
	"NewMemoryPolicyCache": true,
}

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

// methodViolations enforces API-STABILITY.md §§2–3 on every exported method:
// context first, and an options pointer of this package last. Shared
// vocabulary such as *smtp.MailOptions or another package's *ClientOptions is
// not call options.
func methodViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			recv := receiverName(fn)
			key := recv + "." + fn.Name.Name
			if !ast.IsExported(recv) || nonBlockingMethods[key] {
				continue
			}
			params := flattenParams(fn.Type.Params)
			pos := fset.Position(fn.Pos())
			if len(params) == 0 || !isSelector(params[0], "context", "Context") {
				out = append(out, fmt.Sprintf("%s: %s must take context.Context first (API-STABILITY.md §2)", pos, key))
			}
			if len(params) < 2 || !isLocalOptionsPointer(params[len(params)-1]) {
				out = append(out, fmt.Sprintf("%s: %s must take a *...Options of this package last (API-STABILITY.md §3)", pos, key))
			}
		}
	}
	sort.Strings(out)
	return out
}

// functionViolations allows only the exported functions in allowed, each with
// a trailing *...Options of this package.
func functionViolations(fset *token.FileSet, files map[string]*ast.File, allowed map[string]bool) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			pos := fset.Position(fn.Pos())
			if !allowed[fn.Name.Name] {
				out = append(out, fmt.Sprintf("%s: exported function %s is not in exportedFunctions; adding one is an API decision", pos, fn.Name.Name))
				continue
			}
			params := flattenParams(fn.Type.Params)
			if len(params) == 0 || !isLocalOptionsPointer(params[len(params)-1]) {
				out = append(out, fmt.Sprintf("%s: exported function %s must take a *...Options of this package last (API-STABILITY.md §3)", pos, fn.Name.Name))
			}
		}
	}
	sort.Strings(out)
	return out
}

// callbackSignatureViolations enforces API-STABILITY.md §4a on every exported
// function-typed field of every exported struct: one struct parameter, never a
// parameter list, so a callback can grow by adding fields. A blocking callback
// is (context.Context, *T) and returns error last; a notification is (T) or
// (*T). T is a type of this package.
func callbackSignatureViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					ft, ok := field.Type.(*ast.FuncType)
					if !ok {
						continue
					}
					for _, name := range field.Names {
						if !name.IsExported() {
							continue
						}
						if msg := callbackShapeProblem(ft); msg != "" {
							out = append(out, fmt.Sprintf("%s: %s.%s %s (API-STABILITY.md §4a)", fset.Position(name.Pos()), ts.Name.Name, name.Name, msg))
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func callbackShapeProblem(ft *ast.FuncType) string {
	params := flattenParams(ft.Params)
	results := flattenParams(ft.Results)
	switch {
	case len(params) == 2 && isSelector(params[0], "context", "Context"):
		if !isLocalPointer(params[1]) {
			return "must take (context.Context, *T) with T a type of this package"
		}
		if len(results) == 0 || !isIdent(results[len(results)-1], "error") {
			return "blocks on a context and must return error last"
		}
	case len(params) == 1:
		if !isLocalIdent(params[0]) && !isLocalPointer(params[0]) {
			return "must take one struct of this package"
		}
	default:
		return "must take (context.Context, *T) or one struct T of this package, never a parameter list"
	}
	return ""
}

// enumViolations finds every exported type with exported constants, the shape
// of a set, and requires it to be string-backed, so a new int-backed iota set
// cannot slip past a hand-maintained list (API-STABILITY.md §1).
func enumViolations(files map[string]*ast.File) []string {
	withConsts := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			var current string
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				if vs.Type != nil {
					current = ""
					if id, ok := vs.Type.(*ast.Ident); ok {
						current = id.Name
					}
				} else if len(vs.Values) > 0 {
					current = ""
				}
				for _, n := range vs.Names {
					if n.IsExported() && current != "" && ast.IsExported(current) {
						withConsts[current] = true
					}
				}
			}
		}
	}
	var names []string
	for name := range withConsts {
		names = append(names, name)
	}
	sort.Strings(names)
	return openStringViolations(files, names)
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

func TestAPISurfaceMethodsAndFunctions(t *testing.T) {
	fset, files := loadOwnPackage(t)
	for _, v := range methodViolations(fset, files) {
		t.Error(v)
	}
	for _, v := range functionViolations(fset, files, exportedFunctions) {
		t.Error(v)
	}
}

func TestAPISurfaceOpenStringTypes(t *testing.T) {
	_, files := loadOwnPackage(t)
	for _, v := range openStringViolations(files, openStringTypes) {
		t.Error(v)
	}
	for _, v := range enumViolations(files) {
		t.Error(v)
	}
}

func TestAPISurfaceCallbacks(t *testing.T) {
	fset, files := loadOwnPackage(t)
	for _, v := range callbackStructViolations(files, callbackStructs) {
		t.Error(v)
	}
	for _, v := range callbackSignatureViolations(fset, files) {
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

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
	"github.com/kiliant/go-smtp/smtpclient"
	"example.com/dns"
)

type Deliverer struct{}
type Options struct{}
type DeliverOptions struct{}
type Request struct{}
type Event struct{}
type LookupIPRequest struct{}
type IPLookup struct{}
type MXLookup struct{}
type Cache struct{}

// Correct shapes.
func New(opts *Options) (*Deliverer, error) { return nil, nil }
func NewMemoryPolicyCache(opts *MemoryPolicyCacheOptions) PolicyCache { return PolicyCache{} }
func (d *Deliverer) Deliver(ctx context.Context, req *Request, opts *DeliverOptions) error { return nil }

// Drifted shapes.
func (d *Deliverer) Probe(domain string, opts *DeliverOptions) error { return nil }
func (d *Deliverer) Flush(ctx context.Context) error { return nil }
func (d *Deliverer) Lookup(ctx context.Context, opts *DeliverOptions, name string) error { return nil }
func (d *Deliverer) Send(ctx context.Context, req *Request, opts *smtpclient.ClientOptions) error { return nil }
func (d *Deliverer) Mail(ctx context.Context, opts *smtp.MailOptions) error { return nil }
func (c *Cache) Get(name string) error { return nil }
func NewWithResolver(r Resolver) *Deliverer { return nil }
func Deliver(ctx context.Context, req *Request) error { return nil }

type Disposition int
type DANEMode = string
type PolicyKind string
type RouteFailure int

const (
	RouteLoop RouteFailure = iota
	RouteNullMX
)

const (
	KindDANE PolicyKind = "dane"
)

type Resolver struct {
	LookupMX  func(context.Context, string) (MXLookup, error)
	LookupIP  func(context.Context, *LookupIPRequest) (IPLookup, error)
	LookupAny func(ctx context.Context, req *LookupIPRequest) bool
	Backend   net.Resolver
}
type PolicyCache interface{ Load() }

type Options2 struct {
	Trace  func(Event)
	Notify func(name string, err error)
}

var _ smtpwire.Reply
`
	fset, files := parseSource(t, src)

	requireViolations(t, "methods", methodViolations(fset, files),
		[]string{"Deliverer.Flush must take a *...Options", "Deliverer.Lookup must take a *...Options", "Deliverer.Probe must take context.Context first", "Deliverer.Send must take a *...Options of this package", "Deliverer.Mail must take a *...Options of this package", "Cache.Get must take context.Context first"},
		[]string{"Deliverer.Deliver"})

	requireViolations(t, "functions", functionViolations(fset, files, map[string]bool{"New": true, "NewMemoryPolicyCache": true}),
		[]string{"function NewWithResolver is not in", "function Deliver is not in"},
		[]string{"function New ", "function New:", "NewMemoryPolicyCache"})

	requireViolations(t, "open strings", openStringViolations(files, []string{"Disposition", "DANEMode", "PolicyKind", "Missing"}),
		[]string{"Disposition must be", "DANEMode must be", "Missing: open string type is missing"}, []string{"PolicyKind"})

	requireViolations(t, "enums", enumViolations(files),
		[]string{"RouteFailure must be"}, []string{"PolicyKind"})

	requireViolations(t, "callback structs", callbackStructViolations(files, []string{"Resolver", "PolicyCache"}),
		[]string{"Resolver.Backend must be a function field", "PolicyCache must be a struct"}, []string{"Resolver.LookupMX", "Resolver.LookupIP"})

	requireViolations(t, "callback signatures", callbackSignatureViolations(fset, files),
		[]string{"Resolver.LookupMX must take (context.Context, *T)", "Resolver.LookupAny blocks on a context and must return error", "Options2.Notify must take (context.Context, *T) or one struct"},
		[]string{"Resolver.LookupIP", "Options2.Trace"})

	requireViolations(t, "imports", importViolations(fset, files),
		[]string{"must not import github.com/kiliant/go-smtp/internal/smtpwire", "imports example.com/dns"}, []string{"smtpclient", "\"context\"", "\"net\""})
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

// isLocalOptionsPointer reports *XOptions for a type X declared in this
// package; a qualified type is shared vocabulary, not call options.
func isLocalOptionsPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && strings.HasSuffix(id.Name, "Options")
}

func isLocalPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	return ok && isLocalIdent(star.X)
}

// isLocalIdent reports an unqualified exported identifier: a type of this
// package rather than a predeclared one such as string.
func isLocalIdent(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.IsExported()
}

func isIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
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
