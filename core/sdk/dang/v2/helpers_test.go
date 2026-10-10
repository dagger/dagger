package dangv2

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/v2/pkg/dang"

	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
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
	parseDangSource = func(filename string, src []byte) (any, error) {
		parses++
		return origParse(filename, src)
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
	require.Equal(t, want, moduleDeclaredTypeNames(dir, "my-mod"))
	require.Equal(t, 2, *parses, "one parse per .dang file")
	for range 3 {
		require.Equal(t, want, moduleDeclaredTypeNames(dir, "my-mod"))
	}
	require.Equal(t, 2, *parses, "unchanged source is not parsed again")

	// The same source in another directory is the same module source.
	copyDir := t.TempDir()
	writeDangFilesIn(t, copyDir, map[string]string{
		"main.dang":  "type Thing {\n  name: String! { \"thing\" }\n}\n",
		"other.dang": "type Other {\n  n: Int! { 1 }\n}\n",
	})
	require.Equal(t, want, moduleDeclaredTypeNames(copyDir, "my-mod"))
	require.Equal(t, 2, *parses)

	// The module name seeds the main object's name.
	require.Equal(t, []string{"Renamed", "Thing", "Other"}, moduleDeclaredTypeNames(dir, "renamed"))
	require.Equal(t, 4, *parses)

	// Changed content, a renamed file and a new file are each new source.
	writeDangFilesIn(t, dir, map[string]string{"other.dang": "type Another {\n  n: Int! { 1 }\n}\n"})
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(dir, "my-mod"))
	require.Equal(t, 6, *parses)
	require.NoError(t, os.Rename(filepath.Join(dir, "other.dang"), filepath.Join(dir, "zz.dang")))
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(dir, "my-mod"))
	require.Equal(t, 8, *parses)
	writeDangFilesIn(t, dir, map[string]string{"extra.dang": "type Extra {\n  n: Int! { 1 }\n}\n"})
	require.Equal(t, []string{"MyMod", "Extra", "Thing", "Another"}, moduleDeclaredTypeNames(dir, "my-mod"))
	require.Equal(t, 11, *parses)

	// A file that does not parse is skipped, as before, and that is stable
	// for its content too.
	writeDangFilesIn(t, dir, map[string]string{"extra.dang": "type {{{"})
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(dir, "my-mod"))
	require.Equal(t, 14, *parses)
	require.Equal(t, []string{"MyMod", "Thing", "Another"}, moduleDeclaredTypeNames(dir, "my-mod"))
	require.Equal(t, 14, *parses)

	// An unreadable directory still yields the main object's name, and is
	// not remembered.
	missing := filepath.Join(dir, "missing")
	require.Equal(t, []string{"MyMod"}, moduleDeclaredTypeNames(missing, "my-mod"))
	require.NoError(t, os.Mkdir(missing, 0o755))
	writeDangFilesIn(t, missing, map[string]string{"main.dang": "type Late {\n  n: Int! { 1 }\n}\n"})
	require.Equal(t, []string{"MyMod", "Late"}, moduleDeclaredTypeNames(missing, "my-mod"))

	// Nor is a file that cannot be read: the names of the others are returned,
	// and the file's own once it can be read.
	target := filepath.Join(t.TempDir(), "later.dang")
	require.NoError(t, os.Symlink(target, filepath.Join(missing, "zlater.dang")))
	before := *parses
	require.Equal(t, []string{"MyMod", "Late"}, moduleDeclaredTypeNames(missing, "my-mod"))
	require.Equal(t, []string{"MyMod", "Late"}, moduleDeclaredTypeNames(missing, "my-mod"))
	require.Equal(t, before+2, *parses, "an incomplete read is parsed again each time")
	require.NoError(t, os.WriteFile(target, []byte("type Later {\n  n: Int! { 1 }\n}\n"), 0o644))
	require.Equal(t, []string{"MyMod", "Late", "Later"}, moduleDeclaredTypeNames(missing, "my-mod"))
	require.Equal(t, before+4, *parses)
	require.Equal(t, []string{"MyMod", "Late", "Later"}, moduleDeclaredTypeNames(missing, "my-mod"))
	require.Equal(t, before+4, *parses)
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
}
