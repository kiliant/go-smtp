package smtp

// api_surface_test.go — the mechanical gates from docs/API-STABILITY.md,
// live from T02 rather than a later hardening milestone (T02 spec, "this is
// not a hardening-milestone deliverable"). Owned by T02 until T12 takes it
// over (docs/tasks/BOARD.md).
//
// smtpclient does not exist yet — T03 creates it. These gates are written
// against the smtpclient/*.go source on disk via go/parser rather than by
// importing the smtpclient package, for two reasons:
//
//  1. package smtp must import nothing from this module
//     (TestNoModuleImports); importing smtpclient from a smtp_test package
//     would make every `go test` of this package depend on smtpclient
//     compiling, which defeats T01/T02's parallelism with T03+.
//  2. Before smtpclient exists there is nothing to import, and the gates
//     must still compile and report *something* — "will engage as that
//     surface appears" (task brief), not silently pass forever with no
//     evidence they ever ran.
//
// So: if smtpclient/ has no non-test .go files yet, the gates
// TestAPISurface* tests below t.Skip with a message saying why, and
// TestAPISurfaceGateLogic proves the detection logic itself is correct
// against small synthetic sources built in memory — never written under
// smtpclient/, which this task does not own.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// optionsExemptClientMethods lists exported smtpclient.Client command entry
// points that deliberately ship without a *...Options parameter
// (docs/API-STABILITY.md §3). Empty: every entry point built so far takes
// one. Adding a name here is an API decision that needs a written exception
// in docs/API-STABILITY.md, not a fix to this test.
var optionsExemptClientMethods = map[string]bool{}

// nonBlockingClientMethods lists exported smtpclient.Client methods that are
// not command entry points — they neither write to the wire nor wait for a
// reply — so they are exempt from both the context-first gate and the
// options-struct gate (docs/API-STABILITY.md §3). Close is listed with its
// justification written in API-STABILITY.md itself: it matches io.Closer
// and can therefore never take an options parameter. Extension and session
// accessors that read cached state belong here too, as smtpclient adds
// them; each addition is an API decision, not a test fix, and the loosest
// gate in this file — adding a wire-writing method here silences both gates
// at once.
var nonBlockingClientMethods = map[string]bool{
	"Extension": true,
	"Limits":    true,
}

// closerClientMethods are exempt only because their signatures implement a
// standard-library interface. Close performs best-effort RFC 5321 QUIT I/O and
// therefore must never be mislabeled as a non-blocking accessor.
var closerClientMethods = map[string]bool{"Close": true}

// internalPackageSuffixes are the internal/ import paths that must never
// appear in an exported smtpclient (or smtp) signature
// (docs/API-STABILITY.md §6). Adding a new internal/ package to the module
// is a data change to this list, not an API decision, so it lives here
// rather than needing a written exception.
var internalPackageSuffixes = []string{
	"/internal/smtpwire",
	"/internal/smtpsasl",
	"/internal/saslprep",
	"/internal/unicodenorm",
}

// keyedLiteralMarker is the substring (case-insensitive) every doc comment
// on a caller-constructible exported struct must contain
// (docs/API-STABILITY.md §7). Every struct doc comment in this package uses
// the phrase "must use keyed fields" or "constructed ... with keyed fields",
// both of which contain this substring.
const keyedLiteralMarker = "keyed field"

// -----------------------------------------------------------------------
// Gate 1: context-first.

// TestAPISurfaceContextFirst enforces docs/API-STABILITY.md §2: every
// blocking exported Client method takes ctx context.Context as its first
// parameter.
func TestAPISurfaceContextFirst(t *testing.T) {
	fset, files, ok := loadPackageDir(t, "smtpclient")
	if !ok {
		t.Skip("smtpclient package does not exist yet (lands in T03); gate will engage once it does")
	}
	for _, v := range contextFirstViolations(fset, files) {
		t.Error(v)
	}
}

// -----------------------------------------------------------------------
// Gate 2: options struct.

// TestAPISurfaceOptionsStruct enforces docs/API-STABILITY.md §3: every
// command entry point on Client takes a *...Options parameter, even where
// that struct is empty today.
func TestAPISurfaceOptionsStruct(t *testing.T) {
	fset, files, ok := loadPackageDir(t, "smtpclient")
	if !ok {
		t.Skip("smtpclient package does not exist yet (lands in T03); gate will engage once it does")
	}
	for _, v := range optionsStructViolations(fset, files) {
		t.Error(v)
	}
}

// -----------------------------------------------------------------------
// Gate 3: no internal/ leak.

// TestAPISurfaceNoInternalLeak enforces docs/API-STABILITY.md §6: no
// exported signature in package smtp or smtpclient references smtpwire,
// smtpsasl, saslprep or unicodenorm, including as an embedded field or an
// opaque return value. Checked against both packages: smtp can never import
// internal/ at all (TestNoModuleImports is the stronger guarantee for that
// side), but running the same check here is free and keeps one gate
// definition authoritative for "exported signature" everywhere in the
// module.
func TestAPISurfaceNoInternalLeak(t *testing.T) {
	fset, rootFiles, ok := loadPackageDir(t, ".")
	if !ok {
		t.Fatal("could not parse root package smtp")
	}
	for _, v := range internalLeakViolations(fset, rootFiles) {
		t.Error(v)
	}

	if fset2, files, ok := loadPackageDir(t, "smtpclient"); ok {
		for _, v := range internalLeakViolations(fset2, files) {
			t.Error(v)
		}
	} else {
		t.Log("smtpclient package does not exist yet (lands in T03); gate will engage once it does")
	}
}

// -----------------------------------------------------------------------
// Gate 4: keyed-literal doc note.

// TestAPISurfaceKeyedLiteralDocNote enforces docs/API-STABILITY.md §7:
// every exported struct with at least one exported field — the shape a
// caller can construct with a literal — carries a doc comment noting that
// construction must use keyed fields.
func TestAPISurfaceKeyedLiteralDocNote(t *testing.T) {
	_, rootFiles, ok := loadPackageDir(t, ".")
	if !ok {
		t.Fatal("could not parse root package smtp")
	}
	for _, v := range keyedLiteralViolations(rootFiles) {
		t.Error(v)
	}

	if _, files, ok := loadPackageDir(t, "smtpclient"); ok {
		for _, v := range keyedLiteralViolations(files) {
			t.Error(v)
		}
	} else {
		t.Log("smtpclient package does not exist yet (lands in T03); gate will engage once it does")
	}
}

// -----------------------------------------------------------------------
// Gate 5: exported interfaces.

// TestAPISurfaceNoExportedInterfaces enforces docs/API-STABILITY.md §4. The
// public packages currently need no interfaces of their own; standard-library
// interfaces appear directly in signatures instead.
func TestAPISurfaceNoExportedInterfaces(t *testing.T) {
	for _, dir := range []string{".", "smtpclient"} {
		fset, files, ok := loadPackageDir(t, dir)
		if !ok {
			continue
		}
		for _, v := range exportedInterfaceViolations(fset, files) {
			t.Error(v)
		}
	}
}

// -----------------------------------------------------------------------
// Gate 6: public documentation and protocol provenance.

// TestAPISurfaceDocumented enforces the T12 freeze requirement that every
// exported declaration in smtp and smtpclient has documentation naming its
// RFC (or, for Unicode normalization, the applicable UAX). A grouped
// declaration may carry the shared citation on the group comment.
func TestAPISurfaceDocumented(t *testing.T) {
	for _, dir := range []string{".", "smtpclient"} {
		fset, files, ok := loadPackageDir(t, dir)
		if !ok {
			continue
		}
		for _, v := range exportedDocViolations(fset, files) {
			t.Error(v)
		}
	}
}

// TestAPISurfaceExemptionMaps pins the freeze-audited exemptions. Accessors and
// io.Closer are deliberately separate because Close performs RFC 5321 I/O.
func TestAPISurfaceExemptionMaps(t *testing.T) {
	wantNonBlocking := map[string]bool{"Extension": true, "Limits": true}
	wantCloser := map[string]bool{"Close": true}
	if fmt.Sprint(sortedMapKeys(nonBlockingClientMethods)) != fmt.Sprint(sortedMapKeys(wantNonBlocking)) {
		t.Fatalf("nonBlockingClientMethods = %v, want audited accessors %v", sortedMapKeys(nonBlockingClientMethods), sortedMapKeys(wantNonBlocking))
	}
	if fmt.Sprint(sortedMapKeys(closerClientMethods)) != fmt.Sprint(sortedMapKeys(wantCloser)) {
		t.Fatalf("closerClientMethods = %v, want %v", sortedMapKeys(closerClientMethods), sortedMapKeys(wantCloser))
	}
	if len(optionsExemptClientMethods) != 0 {
		t.Fatalf("optionsExemptClientMethods = %v; API-STABILITY.md §3 has no approved command exemptions", sortedMapKeys(optionsExemptClientMethods))
	}
}

// TestAPISurfaceGrowingSets pins the escape hatches required by
// API-STABILITY.md §1: string-backed open values, raw enhanced-code retention,
// and outbound Extra parameters on both command option structs.
func TestAPISurfaceGrowingSets(t *testing.T) {
	_, files, ok := loadPackageDir(t, ".")
	if !ok {
		t.Fatal("could not parse root package smtp")
	}
	for _, name := range []string{"Extension", "BodyType", "DSNNotify", "DSNReturn", "MTPriority", "ORcptAddressType"} {
		if !namedTypeIsIdent(files, name, "string") {
			t.Errorf("%s must remain string-backed and open (API-STABILITY.md §1)", name)
		}
	}
	if !structHasNamedField(files, "EnhancedCode", "Raw") {
		t.Error("EnhancedCode must retain Raw (API-STABILITY.md §1c)")
	}
	for _, name := range []string{"MailOptions", "RcptOptions"} {
		if !structHasNamedField(files, name, "Extra") {
			t.Errorf("%s must retain Extra []Param (API-STABILITY.md §1b)", name)
		}
	}
}

// TestAPISurfaceContentResults enforces API-STABILITY.md §8 for every public
// message-content command. SMTP, LMTP DATA, and BURL all need the same
// per-recipient result shape.
func TestAPISurfaceContentResults(t *testing.T) {
	_, files, ok := loadPackageDir(t, "smtpclient")
	if !ok {
		t.Fatal("could not parse smtpclient")
	}
	for _, name := range []string{"Data", "BURL"} {
		fn := findClientMethod(files, name)
		if fn == nil || fn.Type.Results == nil || len(fn.Type.Results.List) == 0 || optionsTypeName(fn.Type.Results.List[0].Type) != "DataResult" {
			t.Errorf("Client.%s must return smtp.DataResult first (API-STABILITY.md §8)", name)
		}
	}
}

// -----------------------------------------------------------------------
// Gate logic, proven against synthetic sources.

// TestAPISurfaceGateLogic proves the four detectors above are correct,
// using small Client-shaped sources parsed in memory with go/parser. It
// never writes anything under smtpclient/ — T02 does not own that
// directory — which is also why the real TestAPISurface* tests above
// cannot exercise their positive-detection path until T03 lands. This test
// is what makes "the gates are correct" checkable today rather than a claim
// resting on code nobody has written yet.
func TestAPISurfaceGateLogic(t *testing.T) {
	fset := token.NewFileSet()
	parse := func(name, src string) *ast.File {
		t.Helper()
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing synthetic source %s: %v", name, err)
		}
		return f
	}

	good := parse("good.go", `package smtpclient

import (
	"context"

	"github.com/kiliant/go-smtp"
)

type Client struct{}

type FooOptions struct{}

func (c *Client) Foo(ctx context.Context, opts *FooOptions) error { return nil }

// Mail is the shape that actually occurs in the real tree and the one an
// earlier version of hasOptionsParam could not see: MailOptions lives in the
// ROOT package by design, so from smtpclient it can only ever be written
// with a package qualifier. Keep this case — without it the gate passes its
// own self-test while reporting a violation for every correct method.
func (c *Client) Mail(ctx context.Context, from string, opts *smtp.MailOptions) error { return nil }

func (c *Client) Close() error { return nil }
`)

	if v := contextFirstViolations(fset, map[string]*ast.File{"good.go": good}); len(v) != 0 {
		t.Errorf("contextFirstViolations(good) = %v, want none", v)
	}
	if v := optionsStructViolations(fset, map[string]*ast.File{"good.go": good}); len(v) != 0 {
		t.Errorf("optionsStructViolations(good) = %v, want none", v)
	}

	bad := parse("bad.go", `package smtpclient

import "context"

type Client struct{}

func (c *Client) Bar(name string) error { return nil }

func (c *Client) Baz(ctx context.Context) error { return nil }
`)
	badFiles := map[string]*ast.File{"bad.go": bad}

	if v := contextFirstViolations(fset, badFiles); len(v) != 1 {
		t.Errorf("contextFirstViolations(bad) = %v, want exactly 1 (Bar has no ctx first)", v)
	}
	if v := optionsStructViolations(fset, badFiles); len(v) != 2 {
		t.Errorf("optionsStructViolations(bad) = %v, want exactly 2 (Bar and Baz have no *...Options)", v)
	}

	leaky := parse("leaky.go", `package smtpclient

import (
	"context"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

type Client struct{}

func (c *Client) Bad(ctx context.Context) (*smtpwire.Reply, error) { return nil, nil }
`)
	if v := internalLeakViolations(fset, map[string]*ast.File{"leaky.go": leaky}); len(v) != 1 {
		t.Errorf("internalLeakViolations(leaky) = %v, want exactly 1", v)
	}

	clean := parse("clean.go", `package smtpclient

import "context"

type Client struct{}

func (c *Client) Fine(ctx context.Context) error { return nil }
`)
	if v := internalLeakViolations(fset, map[string]*ast.File{"clean.go": clean}); len(v) != 0 {
		t.Errorf("internalLeakViolations(clean) = %v, want none", v)
	}

	undocumented := parse("undoc.go", `package smtpclient

type Thing struct {
	Name string
}
`)
	// Neither the doc note nor the guard: both halves of §7 are reported,
	// so a struct missing both gets two findings rather than one.
	if v := keyedLiteralViolations(map[string]*ast.File{"undoc.go": undocumented}); len(v) != 2 {
		t.Errorf("keyedLiteralViolations(undocumented) = %v, want exactly 2 (missing doc note and missing _ struct{} guard)", v)
	}

	documented := parse("doc.go", `package smtpclient

// Thing is constructed by callers with keyed fields.
type Thing struct {
	Name string

	_ struct{}
}
`)
	if v := keyedLiteralViolations(map[string]*ast.File{"doc.go": documented}); len(v) != 0 {
		t.Errorf("keyedLiteralViolations(documented) = %v, want none", v)
	}

	// The note without the guard: advice a caller can ignore, and the
	// unkeyed literal still compiles. §7 wants both.
	unguarded := parse("unguarded.go", `package smtpclient

// Thing is constructed by callers with keyed fields.
type Thing struct {
	Name string
}
`)
	if v := keyedLiteralViolations(map[string]*ast.File{"unguarded.go": unguarded}); len(v) != 1 {
		t.Errorf("keyedLiteralViolations(unguarded) = %v, want exactly 1 (missing _ struct{} guard)", v)
	}

	// §6 beyond struct fields: a named function type republishing an
	// internal type is an opaque handle, and T03's dial hook is exactly
	// that shape. An alias is the same leak by a shorter route.
	leakyType := parse("leakytype.go", `package smtpclient

import "github.com/kiliant/go-smtp/internal/smtpwire"

type ReplyHook func(*smtpwire.Reply)

type Reply = smtpwire.Reply
`)
	if v := internalLeakViolations(fset, map[string]*ast.File{"leakytype.go": leakyType}); len(v) != 2 {
		t.Errorf("internalLeakViolations(leakyType) = %v, want exactly 2 (named func type and alias)", v)
	}

	leakyVar := parse("leakyvar.go", `package smtpclient

import "github.com/kiliant/go-smtp/internal/smtpwire"

var DefaultLimits smtpwire.Limits
`)
	if v := internalLeakViolations(fset, map[string]*ast.File{"leakyvar.go": leakyVar}); len(v) != 1 {
		t.Errorf("internalLeakViolations(leakyVar) = %v, want exactly 1", v)
	}

	unexported := parse("unexported.go", `package smtpclient

type thing struct {
	Name string
}

type Marker struct {
	name string
}
`)
	if v := keyedLiteralViolations(map[string]*ast.File{"unexported.go": unexported}); len(v) != 0 {
		t.Errorf("keyedLiteralViolations(unexported) = %v, want none (unexported type, and exported type with only unexported fields)", v)
	}

	publicInterface := parse("interface.go", `package smtpclient

type Tracer interface { Trace(string) }
`)
	if v := exportedInterfaceViolations(fset, map[string]*ast.File{"interface.go": publicInterface}); len(v) != 1 {
		t.Errorf("exportedInterfaceViolations = %v, want exactly 1", v)
	}

	undocumentedAPI := parse("api.go", `package smtpclient

type Public string
`)
	if v := exportedDocViolations(fset, map[string]*ast.File{"api.go": undocumentedAPI}); len(v) != 1 {
		t.Errorf("exportedDocViolations(undocumented) = %v, want exactly 1", v)
	}
	documentedAPI := parse("api_doc.go", `package smtpclient

// Public is an RFC 5321 protocol value.
type Public string
`)
	if v := exportedDocViolations(fset, map[string]*ast.File{"api_doc.go": documentedAPI}); len(v) != 0 {
		t.Errorf("exportedDocViolations(documented) = %v, want none", v)
	}
}

// =======================================================================
// Detection logic shared by the real and synthetic-source tests above.

// loadPackageDir parses the non-test .go files directly inside dir and
// returns the first non-test package found there. ok is false when dir does
// not exist or contains no non-test .go files — the "smtpclient doesn't
// exist yet" case.
func loadPackageDir(t *testing.T, dir string) (*token.FileSet, map[string]*ast.File, bool) {
	t.Helper()

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, nil, false
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	for name, pkg := range pkgs {
		if strings.HasSuffix(name, "_test") || len(pkg.Files) == 0 {
			continue
		}
		return fset, pkg.Files, true
	}
	return nil, nil, false
}

// clientMethod is an exported method declared on Client or *Client.
type clientMethod struct {
	name string
	fn   *ast.FuncDecl
}

func findClientMethods(files map[string]*ast.File) []clientMethod {
	var methods []clientMethod
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			if !ast.IsExported(fn.Name.Name) {
				continue
			}
			if recvTypeName(fn.Recv.List[0].Type) != "Client" {
				continue
			}
			methods = append(methods, clientMethod{name: fn.Name.Name, fn: fn})
		}
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].name < methods[j].name })
	return methods
}

func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return ""
	}
}

// isContextContext reports whether expr is the type context.Context.
func isContextContext(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkgIdent.Name == "context" && sel.Sel.Name == "Context"
}

func contextFirstViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	_ = files // methods already carry their positions via fn.Pos()
	var out []string
	for _, m := range findClientMethods(files) {
		if nonBlockingClientMethods[m.name] || closerClientMethods[m.name] {
			continue
		}
		params := m.fn.Type.Params
		pos := fset.Position(m.fn.Pos())
		if params == nil || len(params.List) == 0 {
			out = append(out, fmt.Sprintf("%s: Client.%s has no parameters; a blocking method must take ctx context.Context first (API-STABILITY.md §2)", pos, m.name))
			continue
		}
		first := params.List[0]
		if !isContextContext(first.Type) {
			out = append(out, fmt.Sprintf("%s: Client.%s's first parameter is not context.Context (API-STABILITY.md §2)", pos, m.name))
		}
	}
	return out
}

// hasOptionsParam reports whether params contains a parameter typed as a
// pointer to a type whose name ends in "Options", whether that type is
// named locally (*ClientOptions) or through a package qualifier
// (*smtp.MailOptions).
//
// The qualified form is not an edge case, it is the common one: T02 placed
// MailOptions and RcptOptions in the ROOT package deliberately, because T08
// and T09 both add typed fields to them and neither owns smtpclient. So from
// inside smtpclient those parameters can only ever be spelled
// *smtp.MailOptions — an *ast.SelectorExpr under the star, never an
// *ast.Ident. A version of this function that unwrapped only *ast.Ident
// reported a violation for every correctly written command entry point, and
// the documented remedy for a violation is an optionsExemptClientMethods
// entry. That is precisely the go-imap failure this gate exists to prevent,
// so the SelectorExpr case is load-bearing and TestAPISurfaceGateLogic pins
// it.
func hasOptionsParam(params *ast.FieldList) bool {
	if params == nil {
		return false
	}
	for _, f := range params.List {
		star, ok := f.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if strings.HasSuffix(optionsTypeName(star.X), "Options") {
			return true
		}
	}
	return false
}

// optionsTypeName returns the type name from either an unqualified
// identifier or a package-qualified selector, and "" for anything else.
func optionsTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

func optionsStructViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, m := range findClientMethods(files) {
		if nonBlockingClientMethods[m.name] || closerClientMethods[m.name] || optionsExemptClientMethods[m.name] {
			continue
		}
		if !hasOptionsParam(m.fn.Type.Params) {
			pos := fset.Position(m.fn.Pos())
			out = append(out, fmt.Sprintf("%s: Client.%s is a command entry point with no *...Options parameter (API-STABILITY.md §3)", pos, m.name))
		}
	}
	return out
}

// importAliases maps each local identifier a file uses for an import to
// that import's full path, e.g. "smtpwire" -> ".../internal/smtpwire".
func importAliases(f *ast.File) map[string]string {
	aliases := make(map[string]string, len(f.Imports))
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		aliases[name] = path
	}
	return aliases
}

func internalSuffix(path string) (string, bool) {
	for _, suf := range internalPackageSuffixes {
		if strings.HasSuffix(path, suf) {
			return suf, true
		}
	}
	return "", false
}

// walkForInternalLeak inspects expr (a parameter, result or struct field
// type) for a selector whose package identifier resolves, via imports, to
// one of internalPackageSuffixes.
func walkForInternalLeak(fset *token.FileSet, file string, where string, expr ast.Expr, imports map[string]string, out *[]string) {
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		path, ok := imports[ident.Name]
		if !ok {
			return true
		}
		if suf, leaked := internalSuffix(path); leaked {
			pos := fset.Position(sel.Pos())
			*out = append(*out, fmt.Sprintf("%s:%d: %s exposes %s.%s (%s...%s) in an exported signature (API-STABILITY.md §6)", file, pos.Line, where, ident.Name, sel.Sel.Name, path, suf))
		}
		return true
	})
}

func internalLeakViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for path, f := range files {
		imports := importAliases(f)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !ast.IsExported(d.Name.Name) {
					continue
				}
				if d.Recv != nil && len(d.Recv.List) > 0 && !ast.IsExported(recvTypeName(d.Recv.List[0].Type)) {
					continue
				}
				where := "func " + d.Name.Name
				if d.Type.Params != nil {
					for _, field := range d.Type.Params.List {
						walkForInternalLeak(fset, path, where, field.Type, imports, &out)
					}
				}
				if d.Type.Results != nil {
					for _, field := range d.Type.Results.List {
						walkForInternalLeak(fset, path, where, field.Type, imports, &out)
					}
				}
			case *ast.GenDecl:
				switch d.Tok {
				case token.TYPE:
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok || !ast.IsExported(ts.Name.Name) {
							continue
						}
						where := "type " + ts.Name.Name
						if st, ok := ts.Type.(*ast.StructType); ok && st.Fields != nil {
							// For a struct only the exported fields are
							// surface; an unexported field holding an
							// internal type is fine and is how the client
							// is expected to hold its codec.
							for _, field := range st.Fields.List {
								if !fieldExported(field) {
									continue
								}
								walkForInternalLeak(fset, path, where, field.Type, imports, &out)
							}
							continue
						}
						// Any other exported type publishes its whole
						// definition. §6 forbids an internal type "not as a
						// parameter, return value, embedded field, or
						// opaque handle" — and a named function type is an
						// opaque handle, which matters because T03 ships a
						// dial hook. Covers aliases too: a
						// "type Reply = smtpwire.Reply" would republish the
						// codec under a public name and permanently freeze
						// it.
						walkForInternalLeak(fset, path, where, ts.Type, imports, &out)
					}
				case token.VAR, token.CONST:
					for _, spec := range d.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok || vs.Type == nil {
							continue
						}
						for _, n := range vs.Names {
							if !ast.IsExported(n.Name) {
								continue
							}
							walkForInternalLeak(fset, path, d.Tok.String()+" "+n.Name, vs.Type, imports, &out)
						}
					}
				}
			}
		}
	}
	return out
}

// fieldExported reports whether a struct field is part of the exported API
// surface: an explicitly named exported field, or an embedded field whose
// type name is exported.
func fieldExported(f *ast.Field) bool {
	if len(f.Names) == 0 {
		return ast.IsExported(embeddedName(f.Type))
	}
	for _, n := range f.Names {
		if ast.IsExported(n.Name) {
			return true
		}
	}
	return false
}

// structHasBlankGuard reports whether st contains a "_ struct{}" field, the
// device that makes an unkeyed composite literal of st a compile error and
// therefore makes adding a field a non-breaking change.
func structHasBlankGuard(st *ast.StructType) bool {
	if st.Fields == nil {
		return false
	}
	for _, f := range st.Fields.List {
		if len(f.Names) != 1 || f.Names[0].Name != "_" {
			continue
		}
		if inner, ok := f.Type.(*ast.StructType); ok && (inner.Fields == nil || len(inner.Fields.List) == 0) {
			return true
		}
	}
	return false
}

func embeddedName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return embeddedName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

func keyedLiteralViolations(files map[string]*ast.File) []string {
	var out []string
	for path, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ast.IsExported(ts.Name.Name) {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || !structHasExportedField(st) {
					continue
				}
				doc := ts.Doc
				if doc == nil {
					doc = gd.Doc
				}
				if doc == nil || !strings.Contains(normalizeDocText(doc.Text()), keyedLiteralMarker) {
					out = append(out, fmt.Sprintf("%s: type %s is an exported struct a caller can construct with a literal, but its doc comment does not note that construction must use keyed fields (API-STABILITY.md §7)", path, ts.Name.Name))
				}
				// §7 requires the guard as well as the note. The note is
				// advice; the "_ struct{}" field is what makes an unkeyed
				// literal fail to compile, so a struct carrying only the
				// note still lets a caller write smtp.Param{"SIZE", "1"} —
				// and adding a field then breaks them.
				if !structHasBlankGuard(st) {
					out = append(out, fmt.Sprintf("%s: type %s is an exported struct a caller can construct with a literal, but has no \"_ struct{}\" field to make unkeyed literals a compile error (API-STABILITY.md §7)", path, ts.Name.Name))
				}
			}
		}
	}
	return out
}

// normalizeDocText lowercases doc text and collapses all whitespace runs —
// including the line breaks go/ast.CommentGroup.Text() leaves between
// wrapped comment lines — to single spaces, so a marker phrase that happens
// to wrap across two "//" lines in the source still matches.
func normalizeDocText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func structHasExportedField(st *ast.StructType) bool {
	if st.Fields == nil {
		return false
	}
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			continue // conservative: don't count embedding toward "constructible"
		}
		for _, n := range f.Names {
			if ast.IsExported(n.Name) {
				return true
			}
		}
	}
	return false
}

func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func namedTypeIsIdent(files map[string]*ast.File, name, underlying string) bool {
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != name {
					continue
				}
				id, ok := ts.Type.(*ast.Ident)
				return ok && id.Name == underlying
			}
		}
	}
	return false
}

func structHasNamedField(files map[string]*ast.File, typeName, fieldName string) bool {
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return false
				}
				for _, field := range st.Fields.List {
					for _, name := range field.Names {
						if name.Name == fieldName {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func findClientMethod(files map[string]*ast.File, name string) *ast.FuncDecl {
	for _, method := range findClientMethods(files) {
		if method.name == name {
			return method.fn
		}
	}
	return nil
}

func exportedInterfaceViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ast.IsExported(ts.Name.Name) {
					continue
				}
				if _, ok := ts.Type.(*ast.InterfaceType); ok {
					out = append(out, fmt.Sprintf("%s: exported interface %s is forbidden by API-STABILITY.md §4", fset.Position(ts.Pos()), ts.Name.Name))
				}
			}
		}
	}
	return out
}

func exportedDocViolations(fset *token.FileSet, files map[string]*ast.File) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !ast.IsExported(d.Name.Name) || (d.Recv != nil && !ast.IsExported(recvTypeName(d.Recv.List[0].Type))) {
					continue
				}
				checkProtocolDoc(fset, d.Pos(), d.Name.Name, d.Doc, nil, &out)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if ast.IsExported(s.Name.Name) {
							checkProtocolDoc(fset, s.Pos(), s.Name.Name, s.Doc, d.Doc, &out)
							if strings.HasSuffix(s.Name.Name, "Options") {
								text := ""
								if s.Doc != nil {
									text += s.Doc.Text()
								}
								if d.Doc != nil {
									text += "\n" + d.Doc.Text()
								}
								normalized := normalizeDocText(text)
								if !strings.Contains(normalized, "nil") || !strings.Contains(normalized, "default") {
									out = append(out, fmt.Sprintf("%s: %s must document that nil options means defaults (API-STABILITY.md §3)", fset.Position(s.Pos()), s.Name.Name))
								}
							}
							if st, ok := s.Type.(*ast.StructType); ok {
								for _, field := range st.Fields.List {
									for _, fieldName := range field.Names {
										if !ast.IsExported(fieldName.Name) {
											continue
										}
										if field.Doc == nil && field.Comment == nil {
											out = append(out, fmt.Sprintf("%s: exported field %s.%s has no doc comment", fset.Position(fieldName.Pos()), s.Name.Name, fieldName.Name))
										}
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if ast.IsExported(name.Name) {
								checkProtocolDoc(fset, name.Pos(), name.Name, s.Doc, d.Doc, &out)
							}
						}
					}
				}
			}
		}
	}
	return out
}

func checkProtocolDoc(fset *token.FileSet, pos token.Pos, name string, primary, group *ast.CommentGroup, out *[]string) {
	var text string
	if primary != nil {
		text = primary.Text()
	}
	if group != nil {
		text += "\n" + group.Text()
	}
	if strings.TrimSpace(text) == "" {
		*out = append(*out, fmt.Sprintf("%s: exported symbol %s has no doc comment", fset.Position(pos), name))
		return
	}
	normalized := normalizeDocText(text)
	if !strings.Contains(normalized, "rfc ") && !strings.Contains(normalized, "uax ") {
		*out = append(*out, fmt.Sprintf("%s: exported symbol %s doc comment names no RFC or UAX provenance", fset.Position(pos), name))
	}
}
