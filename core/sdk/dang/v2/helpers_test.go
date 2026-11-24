package dangv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"

	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/dagql/call"
)

// inferReport builds the kind of error dang.RunDir returns for a module with
// inference errors: an InferenceErrors of source-located errors whose
// rendering carries multi-line, ANSI-colored source excerpts.
func inferReport(inner error) *dang.InferenceErrors {
	return &dang.InferenceErrors{
		Errors: []error{
			dang.NewSourceError(inner,
				&dang.SourceLocation{Filename: "main.dang", Line: 2, Column: 3, Length: 4},
				"type Foo {\n  bogus: Int! { 1 }\n}\n"),
		},
	}
}

func TestReportDangSourceError(t *testing.T) {
	t.Parallel()

	report := inferReport(errors.New("unknown type Bogus"))
	require.True(t, isDangSourceError(report))

	var stderr bytes.Buffer
	err := reportDangSourceError(&stderr, report)

	// The rendered report lands on stderr verbatim, newline-terminated
	// exactly once.
	require.Equal(t, strings.TrimRight(report.Error(), "\n")+"\n", stderr.String())
	require.Contains(t, stderr.String(), "unknown type Bogus")
	require.Contains(t, stderr.String(), "main.dang:2:3")
	require.Contains(t, stderr.String(), "\033[") // Dang's ANSI styling is preserved

	// The span error is short and carries none of the report.
	require.Equal(t, "unknown type Bogus", err.Error())
	require.NotContains(t, err.Error(), "main.dang")

	// The original is still reachable for errors.As-based handling.
	var inferErrs *dang.InferenceErrors
	require.ErrorAs(t, err, &inferErrs)
	require.Same(t, report, inferErrs)
}

// TestDangSourceErrorKeepsGraphQLExtraction locks in that ConvertError still
// sees a GraphQL error raised while evaluating a module's top-level source
// through the short load error, so its message and extensions survive.
func TestDangSourceErrorKeepsGraphQLExtraction(t *testing.T) {
	t.Parallel()

	gqlErr := &gqlerror.Error{
		Message:    "boom",
		Extensions: map[string]any{"exitCode": 2},
	}
	evalErr := dang.NewSourceError(gqlErr,
		&dang.SourceLocation{Filename: "main.dang", Line: 1, Column: 1, Length: 1},
		"container.from(\"nope\").sync\n")
	require.True(t, isDangSourceError(evalErr))

	err := reportDangSourceError(&bytes.Buffer{}, evalErr)
	converted := dangshared.ConvertError(err)
	require.Equal(t, "boom", converted.Message)
	require.NotContains(t, converted.Message, "main.dang")
	require.Len(t, converted.Values, 1)
	require.Equal(t, "exitCode", converted.Values[0].Name)
	require.JSONEq(t, "2", string(converted.Values[0].Value))
}

func TestDangSourceMessage(t *testing.T) {
	t.Parallel()

	message := "unresolved type: IntentionallyUndefinedType"
	inner := errors.New(message)
	sourceErr := inferReport(inner).Errors[0]
	raisedValue := dang.NewObject(nil)
	raisedValue.Bind("message", dang.StringValue{Val: "raised failure"}, dang.PublicVisibility)

	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"source", sourceErr, message},
		{"infer", &dang.InferError{Inner: sourceErr}, message},
		{"multiple", &dang.InferenceErrors{Errors: []error{sourceErr, errors.New("second failure")}}, message},
		{"wrapped aggregate", fmt.Errorf("load module: %w", inferReport(inner)), message},
		{"joined", errors.Join(sourceErr, errors.New("second failure")), message},
		{"raised", &dang.RaisedError{Value: raisedValue}, "raised failure"},
		{"plain context", inferReport(fmt.Errorf("invalid argument: %w", inner)), "invalid argument: " + message},
		{"multiline and ANSI", inferReport(errors.New("\033[31mfirst line\033[0m\n\tsecond line\r\n")), "first line second line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := reportDangSourceError(&stderr, tc.err)
			require.EqualError(t, err, tc.want)
			require.Same(t, tc.err, errors.Unwrap(err))
			require.Equal(t, strings.TrimRight(tc.err.Error(), "\n")+"\n", stderr.String())
		})
	}
}

func TestDangSourceMessageFromModule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`type Broken {
  hello: IntentionallyUndefinedType! { "hi" }
}
`), 0o644))
	_, report := runDangDirForModuleTypes(t.Context(), dir)
	require.Error(t, report)
	require.True(t, isDangSourceError(report))
	var stderr bytes.Buffer
	err := reportDangSourceError(&stderr, report)
	require.EqualError(t, err, "unresolved type: IntentionallyUndefinedType")
	require.Contains(t, stderr.String(), "main.dang:")
	require.Contains(t, stderr.String(), `hello: IntentionallyUndefinedType! { "hi" }`)
}

func TestIsDangSourceErrorIgnoresInfrastructureErrors(t *testing.T) {
	t.Parallel()

	require.False(t, isDangSourceError(errors.New("no .dang files found in directory: /src")))
	require.False(t, isDangSourceError(fmt.Errorf("read module entrypoint directory: %w", errors.New("permission denied"))))
}

// countDangParses counts calls to parseDangSource until the test ends, and
// gives the test its own empty memo of declared type names. Tests using it
// must not run in parallel.
func countDangParses(t *testing.T) *int {
	t.Helper()
	var parses int
	origParse, origNames := parseDangSource, declaredTypeNames
	parseDangSource = func(ctx context.Context, filename string) (*dang.FileBlock, error) {
		parses++
		return origParse(ctx, filename)
	}
	fresh, err := lru.New[string, []string](8)
	require.NoError(t, err)
	declaredTypeNames = fresh
	t.Cleanup(func() { parseDangSource, declaredTypeNames = origParse, origNames })
	return &parses
}

func writeDangFilesIn(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
}

// A function call asks for the module's declared type names every time, only
// to find them all in the runtime schema. While the memo holds the names for
// the source's content, they must come from it and not from parsing the
// source again.
func TestModuleDeclaredTypeNamesMemoized(t *testing.T) {
	parses := countDangParses(t)
	dir := t.TempDir()
	writeDangFilesIn(t, dir, map[string]string{
		"main.dang":  "type Thing {\n  name: String! { \"thing\" }\n}\n",
		"other.dang": "type Other {\n  n: Int! { 1 }\n}\n",
		"notes.txt":  "not source",
	})

	want := []string{"MyMod", "Thing", "Other"}
	require.Equal(t, want, moduleDeclaredTypeNames(t.Context(), dir, "my-mod"))
	require.Equal(t, 2, *parses, "one parse per .dang file")
	for range 3 {
		require.Equal(t, want, moduleDeclaredTypeNames(t.Context(), dir, "my-mod"))
	}
	require.Equal(t, 2, *parses, "unchanged source is not parsed again")

	// The same source in another directory is the same module source.
	copyDir := t.TempDir()
	writeDangFilesIn(t, copyDir, map[string]string{
		"main.dang":  "type Thing {\n  name: String! { \"thing\" }\n}\n",
		"other.dang": "type Other {\n  n: Int! { 1 }\n}\n",
	})
	require.Equal(t, want, moduleDeclaredTypeNames(t.Context(), copyDir, "my-mod"))
	require.Equal(t, 2, *parses)

	// The module name seeds the main object's name.
	require.Equal(t, []string{"Renamed", "Thing", "Other"}, moduleDeclaredTypeNames(t.Context(), dir, "renamed"))
	require.Equal(t, 4, *parses)

	// Changed content, a renamed file and a new file are each new source.
	writeDangFilesIn(t, dir, map[string]string{"other.dang": "type Another {\n  n: Int! { 1 }\n}\n"})
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(t.Context(), dir, "my-mod"))
	require.Equal(t, 6, *parses)
	require.NoError(t, os.Rename(filepath.Join(dir, "other.dang"), filepath.Join(dir, "zz.dang")))
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(t.Context(), dir, "my-mod"))
	require.Equal(t, 8, *parses)
	writeDangFilesIn(t, dir, map[string]string{"extra.dang": "type Extra {\n  n: Int! { 1 }\n}\n"})
	require.Equal(t, []string{"MyMod", "Extra", "Thing", "Another"}, moduleDeclaredTypeNames(t.Context(), dir, "my-mod"))
	require.Equal(t, 11, *parses)

	// A file that does not parse is skipped, as before, and that is stable
	// for its content too.
	writeDangFilesIn(t, dir, map[string]string{"extra.dang": "type {{{"})
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(t.Context(), dir, "my-mod"))
	require.Equal(t, 14, *parses)
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(t.Context(), dir, "my-mod"))
	require.Equal(t, 14, *parses)

	// An unreadable directory still yields the main object's name, and is
	// not remembered.
	missing := filepath.Join(dir, "missing")
	require.Equal(t, []string{"MyMod"}, moduleDeclaredTypeNames(t.Context(), missing, "my-mod"))
	require.NoError(t, os.Mkdir(missing, 0o755))
	writeDangFilesIn(t, missing, map[string]string{"main.dang": "type Late {\n  n: Int! { 1 }\n}\n"})
	require.Equal(t, []string{"MyMod", "Late"}, moduleDeclaredTypeNames(t.Context(), missing, "my-mod"))

	// Nor is a file that cannot be read: the names of the others are returned,
	// and the file's own once it can be read.
	target := filepath.Join(t.TempDir(), "later.dang")
	require.NoError(t, os.Symlink(target, filepath.Join(missing, "zlater.dang")))
	before := *parses
	require.Equal(t, []string{"MyMod", "Late"}, moduleDeclaredTypeNames(t.Context(), missing, "my-mod"))
	require.Equal(t, []string{"MyMod", "Late"}, moduleDeclaredTypeNames(t.Context(), missing, "my-mod"))
	require.Equal(t, before+2, *parses, "an incomplete read is parsed again each time")
	require.NoError(t, os.WriteFile(target, []byte("type Later {\n  n: Int! { 1 }\n}\n"), 0o644))
	require.Equal(t, []string{"MyMod", "Late", "Later"}, moduleDeclaredTypeNames(t.Context(), missing, "my-mod"))
	require.Equal(t, before+4, *parses)
	require.Equal(t, []string{"MyMod", "Late", "Later"}, moduleDeclaredTypeNames(t.Context(), missing, "my-mod"))
	require.Equal(t, before+4, *parses)

	// Nor is a dang.toml that cannot be read, and repairing it alone finds
	// the names of the source it now lets parse.
	configured := t.TempDir()
	writeDangFilesIn(t, configured, map[string]string{
		"main.dang": "type Thing {\n  name: String! { \"thing\" }\n}\n",
		"dang.toml": "dang = \n",
	})
	require.Equal(t, []string{"MyMod"}, moduleDeclaredTypeNames(t.Context(), configured, "my-mod"))
	writeDangFilesIn(t, configured, map[string]string{"dang.toml": "dang = \"2.1\"\n"})
	require.Equal(t, []string{"MyMod", "Thing"}, moduleDeclaredTypeNames(t.Context(), configured, "my-mod"))
}

// A module's prebuild parses its source while its dependencies load
// (PrebuildRuntime). When the module's types are read before that parse
// ends, they wait for it rather than parse the same source a second time.
func TestModuleDeclaredTypeNamesSharesParse(t *testing.T) {
	origParse, origNames := parseDangSource, declaredTypeNames
	fresh, err := lru.New[string, []string](8)
	require.NoError(t, err)
	declaredTypeNames = fresh
	var parses atomic.Int32
	second := make(chan struct{})
	var secondOnce sync.Once
	parseDangSource = func(ctx context.Context, filename string) (*dang.FileBlock, error) {
		if parses.Add(1) == 1 {
			// Hold the first parse until a second one starts, or until the
			// other call has long since asked for the names.
			select {
			case <-second:
			case <-time.After(200 * time.Millisecond):
			}
		} else {
			secondOnce.Do(func() { close(second) })
		}
		return origParse(ctx, filename)
	}
	t.Cleanup(func() { parseDangSource, declaredTypeNames = origParse, origNames })

	dir := t.TempDir()
	writeDangFilesIn(t, dir, map[string]string{"main.dang": "type Thing {\n  name: String! { \"thing\" }\n}\n"})
	names := make([][]string, 2)
	var wg sync.WaitGroup
	for i := range names {
		wg.Go(func() { names[i] = moduleDeclaredTypeNames(t.Context(), dir, "my-mod") })
	}
	wg.Wait()
	for _, got := range names {
		require.Equal(t, []string{"MyMod", "Thing"}, got)
	}
	require.Equal(t, int32(1), parses.Load(), "both calls share one parse")
}

// The prebuild is canceled when its module's load returns, perhaps while it
// is still parsing, and it then removes the source it parses from. The names
// it records for that source must still be all of the source's.
func TestModuleDeclaredTypeNamesCanceledParseCompletes(t *testing.T) {
	origParse, origNames := parseDangSource, declaredTypeNames
	fresh, err := lru.New[string, []string](8)
	require.NoError(t, err)
	declaredTypeNames = fresh
	ctx, cancel := context.WithCancel(t.Context())
	removed := make(chan struct{})
	var first atomic.Bool
	parseDangSource = func(parseCtx context.Context, filename string) (*dang.FileBlock, error) {
		if first.CompareAndSwap(false, true) {
			cancel()
			// Give a caller that returns once canceled the time to remove
			// its source before the next file is read.
			select {
			case <-removed:
			case <-time.After(200 * time.Millisecond):
			}
		}
		return origParse(parseCtx, filename)
	}
	t.Cleanup(func() { parseDangSource, declaredTypeNames = origParse, origNames })

	files := map[string]string{
		"a.dang": "type Alpha {\n  n: Int! { 1 }\n}\n",
		"b.dang": "type Beta {\n  n: Int! { 2 }\n}\n",
	}
	src := filepath.Join(t.TempDir(), "src")
	require.NoError(t, os.Mkdir(src, 0o755))
	writeDangFilesIn(t, src, files)
	moduleDeclaredTypeNames(ctx, src, "my-mod")
	require.NoError(t, os.RemoveAll(src))
	close(removed)

	// The same source, loaded again from another directory.
	other := t.TempDir()
	writeDangFilesIn(t, other, files)
	require.Equal(t, []string{"MyMod", "Alpha", "Beta"}, moduleDeclaredTypeNames(t.Context(), other, "my-mod"))
}

// Object directives are put back on the environment by parsing every source
// file again. Only type registration reads them, so a function call must not
// pay for that parse.
func TestRetainDangObjectDirectivesOnlyForTypeDefs(t *testing.T) {
	dir := t.TempDir()
	writeDangFilesIn(t, dir, map[string]string{
		"main.dang": "directive @marked on OBJECT\n\ntype Thing @marked {\n  name: String! { \"thing\" }\n}\n\ntype Plain {\n  n: Int! { 1 }\n}\n",
	})
	objectDirectives := func(env dang.ValueScope, name string) []*dang.DirectiveApplication {
		value, found, err := env.Lookup(t.Context(), name)
		require.NoError(t, err)
		require.True(t, found)
		return value.(*dang.ConstructorFunction).ObjectType.GetDirectives("")
	}

	env, err := runDangDirForModuleTypes(t.Context(), dir)
	require.NoError(t, err)
	require.Empty(t, objectDirectives(env, "Thing"), "Dang itself does not retain object directives")

	parses := countDangParses(t)
	require.NoError(t, retainDangObjectDirectives(t.Context(), env, dir, false))
	require.Zero(t, *parses, "a function call does not parse the source again")
	require.Empty(t, objectDirectives(env, "Thing"))

	require.NoError(t, retainDangObjectDirectives(t.Context(), env, dir, true))
	require.Equal(t, 1, *parses)
	directives := objectDirectives(env, "Thing")
	require.Len(t, directives, 1)
	require.Equal(t, "marked", directives[0].Name)
	require.Empty(t, objectDirectives(env, "Plain"))

	// The next type registration of the same source reads its syntax from the
	// engine's syntax cache, and still finds the directives.
	env, err = runDangDirForModuleTypes(withDangSyntaxCache(t.Context()), dir)
	require.NoError(t, err)
	require.NoError(t, retainDangObjectDirectives(t.Context(), env, dir, true))
	directives = objectDirectives(env, "Thing")
	require.Len(t, directives, 1)
	require.Equal(t, "marked", directives[0].Name)
}

func TestDangDeclaredTypeOfInterface(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDangFilesIn(t, dir, map[string]string{
		"main.dang": "interface Greeter {\n  pub greet(name: String!): String!\n}\n",
	})
	env, err := runDangDirForModuleTypes(t.Context(), dir)
	require.NoError(t, err)

	// Registering a module's types reads each public binding the way
	// initDangModule does: the interface's name is bound to a value whose
	// declared type must be the interface itself, with its methods, and the
	// same type the module names, so it is marked as local.
	var iface *dang.Type
	for _, binding := range env.Bindings(dang.PublicVisibility) {
		if binding.Key != "Greeter" {
			continue
		}
		obj, ok := binding.Value.(*dang.Object)
		require.True(t, ok, "Greeter is bound to %T", binding.Value)
		iface, ok = dangDeclaredType(obj)
		require.True(t, ok)
	}
	require.NotNil(t, iface, "Greeter is not a public binding")
	require.Equal(t, dang.InterfaceKind, iface.Kind)

	var methods []string
	for name := range iface.Bindings(dang.PublicVisibility) {
		methods = append(methods, name)
	}
	require.Equal(t, []string{"greet"}, methods)

	module, ok := dangEvalModule(env)
	require.True(t, ok)
	named, found := module.NamedType("Greeter")
	require.True(t, found)
	require.Same(t, named, iface)
	require.True(t, collectDangLocalTypes(env).contains(iface))
}

func TestConvertObjectIDKeepsHandleID(t *testing.T) {
	t.Parallel()

	env := dang.NewValueScope(dang.NewType("Test", dang.ObjectKind))
	env.Bind("node", dang.GraphQLFunction{
		Name:   "node",
		Field:  &introspection.Field{Name: "node"},
		Client: failingClient{},
		Schema: contractTestSchema(),
	}, dang.PublicVisibility)
	conv := dangConverter{env: env}
	dirType := dang.NewType("Directory", dang.ObjectKind)
	gqlType := &ast.Type{NamedType: "Directory", NonNull: true}

	// failingClient rejects every request, so a handle passes only if it is
	// handed back without asking the API for it.
	handle, err := call.NewEngineResultID(7, call.NewType(gqlType)).Encode()
	require.NoError(t, err)
	value, err := conv.convert(t.Context(), handle, hm.NonNullType{Type: dirType})
	require.NoError(t, err)
	gotJSON, err := json.Marshal(value)
	require.NoError(t, err)
	require.JSONEq(t, fmt.Sprintf("%q", handle), string(gotJSON))

	// A recipe is still loaded, so the module passes on the handle node(id:)
	// evaluates it to.
	recipe, err := call.New().Append(gqlType, "directory").Encode()
	require.NoError(t, err)
	value, err = conv.convert(t.Context(), recipe, hm.NonNullType{Type: dirType})
	require.NoError(t, err)
	_, err = json.Marshal(value)
	require.ErrorContains(t, err, "unexpected API request")
}

// The engine reads a module's dang.toml only up to its module directory, but
// Dang walks further up for the one that decides the source's version. A
// module source that a dang.toml above its module directory would govern runs
// from an isolated copy, so the engine's version decides.
func TestDangSourceDir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	moduleDir := filepath.Join(root, "mod")
	srcDir := filepath.Join(moduleDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	writeDangFilesIn(t, srcDir, map[string]string{"main.dang": "pub x: Int! = 1\n", "notes.txt": "not source"})

	dir, cleanup, err := dangSourceDir(srcDir, moduleDir)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, srcDir, dir, "no dang.toml: the source runs in place")

	require.NoError(t, os.WriteFile(filepath.Join(root, "dang.toml"), []byte("dang = \"1.0\"\n"), 0o644))
	dir, cleanup, err = dangSourceDir(srcDir, moduleDir)
	require.NoError(t, err)
	require.NotEqual(t, srcDir, dir, "a dang.toml above the module directory: the source runs isolated")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "main.dang", entries[0].Name())
	configPath, _, err := dang.FindProjectConfig(dir)
	require.NoError(t, err)
	require.NotEqual(t, filepath.Join(root, "dang.toml"), configPath)
	cleanup()
	_, err = os.Stat(dir)
	require.True(t, os.IsNotExist(err), "cleanup removes the copy")

	for _, governing := range []string{moduleDir, srcDir} {
		require.NoError(t, os.WriteFile(filepath.Join(governing, "dang.toml"), []byte(""), 0o644))
		dir, cleanup, err = dangSourceDir(srcDir, moduleDir)
		require.NoError(t, err)
		cleanup()
		require.Equal(t, srcDir, dir, "a dang.toml the engine reads: the source runs in place")
		require.NoError(t, os.Remove(filepath.Join(governing, "dang.toml")))
	}

	// A source directory outside the module directory has only its own.
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "dang.toml"), []byte(""), 0o644))
	dir, cleanup, err = dangSourceDir(outside, moduleDir)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, outside, dir)
}

// legacySelectionSource is Dang 1.0 source: `.{a, b}` is object selection,
// which no later version parses.
const legacySelectionSource = "directive @marked on OBJECT\n\n" +
	"type Pair @marked {\n  pub a: Int! = 1\n  pub b: Int! = 2\n}\n\n" +
	"type Picker {\n  pub picked: Int! {\n    let p = Pair.{a, b}\n    p.a\n  }\n}\n"

// The engine's own parses of a module's source (declared type names, object
// directives) parse it as the Dang version it is written in, as Dang does: a
// Dang 1.0 module has types and directives too.
func TestModuleSourceParsesAtDangVersion(t *testing.T) {
	dir := t.TempDir()
	writeDangFilesIn(t, dir, map[string]string{"main.dang": legacySelectionSource})

	countDangParses(t) // a fresh memo
	require.Equal(t, []string{"Legacy", "Pair", "Picker"}, moduleDeclaredTypeNames(dang.ContextWithVersion(t.Context(), dang.Version1_0), dir, "legacy"))
	require.Equal(t, []string{"Legacy"}, moduleDeclaredTypeNames(dang.ContextWithVersion(t.Context(), dang.CurrentVersion), dir, "legacy"),
		"the names are keyed by version: the source does not parse as the current version")

	ctx := dang.ContextWithVersion(t.Context(), dang.Version1_0)
	env, err := runDangDirForModuleTypes(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, retainDangObjectDirectives(ctx, env, dir, true))
	value, found, err := env.Lookup(ctx, "Pair")
	require.NoError(t, err)
	require.True(t, found)
	directives := value.(*dang.ConstructorFunction).ObjectType.GetDirectives("")
	require.Len(t, directives, 1)
	require.Equal(t, "marked", directives[0].Name)

	require.Error(t, retainDangObjectDirectives(dang.ContextWithVersion(ctx, dang.CurrentVersion), env, dir, true))
}
