package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// objectToolsTestSchema builds a small module-shaped schema for exercising the
// object-tools generation helpers. Object arguments cross the wire as `ID`
// scalars carrying an @expectedType directive, mirroring the real schema.
func objectToolsTestSchema(t *testing.T) *ast.Schema {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{
		Name: "test.graphql",
		Input: `
directive @expectedType(name: String!) on ARGUMENT_DEFINITION

type Query { doug: Doug! }

type Workspace { id: ID! }
type Changeset { id: ID! }
type LLM { id: ID! }
type Container { id: ID! }
type Directory { id: ID! }
type Secret { id: ID! }

"A coding agent."
type Doug {
  id: ID!
  sync: Doug!

  "Read a file."
  read(
    source: ID! @expectedType(name: "Workspace"),
    filePath: String!,
    offset: Int! = 0,
    date: String = null,
  ): String!

  "Write a file."
  write(
    source: ID! @expectedType(name: "Workspace"),
    filePath: String!,
    contents: String!,
  ): Changeset!

  "Update the TODO list."
  todoWrite(pending: [String!]! = []): Doug!

  "Build an agent — its LLM! arg is auto-injected, so it IS eligible."
  agent(base: ID! @expectedType(name: "LLM")): LLM!

  "Apply a changeset — requires a non-liftable object arg, so ineligible."
  apply(changes: ID! @expectedType(name: "Changeset")): Doug!

  "Run a command — its required Container arg is LIFTABLE, so eligible."
  exec(
    cmd: String!,
    sandbox: ID! @expectedType(name: "Container"),
  ): String!

  "Run everywhere — a required LIST of object args is not liftable."
  execAll(
    cmd: String!,
    sandboxes: [ID!]! @expectedType(name: "Container"),
  ): String!

  "Debug in a sandbox — an optional liftable arg."
  debug(sandbox: ID @expectedType(name: "Container")): Doug!

  "Mount a directory — Directory is addressable but NOT liftable."
  withDir(dir: ID @expectedType(name: "Directory")): Doug!

  "Import a directory — a required non-liftable arg, so ineligible."
  importDir(dir: ID! @expectedType(name: "Directory")): Doug!

  "Authenticate — a required Secret arg, deliberately not liftable."
  withToken(token: ID! @expectedType(name: "Secret")): Doug!

  old: String! @deprecated(reason: "gone")
}
`,
	})
	require.NoError(t, err)
	return schema
}

func fieldByName(def *ast.Definition, name string) *ast.FieldDefinition {
	for _, f := range def.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func TestObjectToolEligible(t *testing.T) {
	schema := objectToolsTestSchema(t)
	doug := schema.Types["Doug"]

	// Methods whose required args are all scalars (or the auto-injected Workspace)
	// are eligible.
	require.True(t, objectToolEligible(fieldByName(doug, "read"), nil))
	require.True(t, objectToolEligible(fieldByName(doug, "write"), nil))
	require.True(t, objectToolEligible(fieldByName(doug, "todoWrite"), nil))

	// A required object-typed argument disqualifies the method — the model has no
	// handle to pass.
	require.False(t, objectToolEligible(fieldByName(doug, "apply"), nil))

	// ...except when the engine fills it in: an `LLM!` arg is auto-injected with
	// the conversation making the call, so it does not disqualify.
	require.True(t, objectToolEligible(fieldByName(doug, "agent"), nil))

	// ...or when the type is LIFTABLE: a required Container arg renders as an
	// address string and is lifted via the core Address API at dispatch time.
	require.True(t, objectToolEligible(fieldByName(doug, "exec"), nil))

	// A required LIST of liftable objects is not lifted, so it still
	// disqualifies.
	require.False(t, objectToolEligible(fieldByName(doug, "execAll"), nil))

	// An optional object arg never disqualified; still doesn't — liftable or
	// not.
	require.True(t, objectToolEligible(fieldByName(doug, "debug"), nil))
	require.True(t, objectToolEligible(fieldByName(doug, "withDir"), nil))

	// Directory IS address-resolvable (the CLI lifts it for flags), but it is
	// NOT in the liftableTypes allowlist — its Address decoder falls back to
	// HOST paths, a capability the model must not gain from a string — so a
	// required Directory arg disqualifies.
	require.False(t, objectToolEligible(fieldByName(doug, "importDir"), nil))

	// Secret is deliberately not liftable: Address.secret resolves env:// /
	// file:// / op:// URIs, so lifting would let the model MINT secrets by
	// guessing URIs instead of only receiving handles it was given. Each type
	// joins the allowlist only after its own capability review — see
	// hack/designs/sandboxes.md §4, "The liftable set is a capability
	// decision".
	require.False(t, objectToolEligible(fieldByName(doug, "withToken"), nil))

	// except drops a method by name.
	require.False(t, objectToolEligible(fieldByName(doug, "read"), []string{"read"}))

	// Reserved / internal / deprecated fields are never tools.
	require.False(t, objectToolEligible(fieldByName(doug, "id"), nil))
	require.False(t, objectToolEligible(fieldByName(doug, "sync"), nil))
	require.False(t, objectToolEligible(fieldByName(doug, "old"), nil))
}

func TestObjectMethodSchema(t *testing.T) {
	schema := objectToolsTestSchema(t)
	doug := schema.Types["Doug"]

	readSchema, err := objectMethodSchema(schema, fieldByName(doug, "read"))
	require.NoError(t, err)
	props := readSchema["properties"].(map[string]any)

	// The auto-injected Workspace argument is hidden from the model.
	require.NotContains(t, props, "source")

	// Scalar args are surfaced with their JSON types; required tracks non-null
	// args without a default.
	require.Equal(t, "string", props["filePath"].(map[string]any)["type"])
	require.Equal(t, "integer", props["offset"].(map[string]any)["type"])
	require.EqualValues(t, 0, props["offset"].(map[string]any)["default"])
	require.Equal(t, "string", props["date"].(map[string]any)["type"])
	require.Contains(t, props["date"].(map[string]any), "default")
	require.Nil(t, props["date"].(map[string]any)["default"])
	require.Equal(t, []string{"filePath"}, readSchema["required"])
	require.Equal(t, false, readSchema["additionalProperties"])

	// A list arg with a default is optional and rendered as an array of scalars.
	todoSchema, err := objectMethodSchema(schema, fieldByName(doug, "todoWrite"))
	require.NoError(t, err)
	todoProps := todoSchema["properties"].(map[string]any)
	pending := todoProps["pending"].(map[string]any)
	require.Equal(t, "array", pending["type"])
	require.Equal(t, "string", pending["items"].(map[string]any)["type"])
	require.NotContains(t, todoSchema, "required") // pending has a default

	// A required liftable object arg renders as a string, described as an
	// address — with the type's own syntax hint from liftableTypes — and is
	// required.
	const containerHint = `(Container address: an image ref like "golang:1.26", an installed module function like "mymod:dev", or a Container ID from a prior tool result)`
	execSchema, err := objectMethodSchema(schema, fieldByName(doug, "exec"))
	require.NoError(t, err)
	execProps := execSchema["properties"].(map[string]any)
	sandbox := execProps["sandbox"].(map[string]any)
	require.Equal(t, "string", sandbox["type"])
	require.Equal(t, containerHint, sandbox["description"])
	require.ElementsMatch(t, []string{"cmd", "sandbox"}, execSchema["required"])

	// An optional liftable object arg says "address" too — dispatch lifts
	// both.
	debugSchema, err := objectMethodSchema(schema, fieldByName(doug, "debug"))
	require.NoError(t, err)
	debug := debugSchema["properties"].(map[string]any)["sandbox"].(map[string]any)
	require.Equal(t, "string", debug["type"])
	require.Equal(t, containerHint, debug["description"])

	// An optional arg of an addressable-but-not-liftable type keeps the ID
	// convention: the model may hand back an ID from a prior tool result, but
	// is not invited to write an address (dispatch would refuse it anyway).
	dirSchema, err := objectMethodSchema(schema, fieldByName(doug, "withDir"))
	require.NoError(t, err)
	dir := dirSchema["properties"].(map[string]any)["dir"].(map[string]any)
	require.Equal(t, "string", dir["type"])
	require.Equal(t, "(Directory ID)", dir["description"])

	// A non-addressable object arg keeps the ID convention.
	applySchema, err := objectMethodSchema(schema, fieldByName(doug, "apply"))
	require.NoError(t, err)
	changes := applySchema["properties"].(map[string]any)["changes"].(map[string]any)
	require.Equal(t, "(Changeset ID)", changes["description"])

	// A LIST of liftable objects is not lifted, so it keeps the ID
	// convention as well.
	execAllSchema, err := objectMethodSchema(schema, fieldByName(doug, "execAll"))
	require.NoError(t, err)
	sandboxes := execAllSchema["properties"].(map[string]any)["sandboxes"].(map[string]any)
	require.Equal(t, "array", sandboxes["type"])
	require.Equal(t, "(Container ID)", sandboxes["description"])
}

func TestLiftableObjectArg(t *testing.T) {
	schema := objectToolsTestSchema(t)
	doug := schema.Types["Doug"]

	arg := func(field, name string) *ast.ArgumentDefinition {
		f := fieldByName(doug, field)
		require.NotNil(t, f)
		return f.Arguments.ForName(name)
	}

	// Liftable types qualify, required or optional.
	typeName, ok := liftableObjectArg(arg("exec", "sandbox"))
	require.True(t, ok)
	require.Equal(t, "Container", typeName)
	typeName, ok = liftableObjectArg(arg("debug", "sandbox"))
	require.True(t, ok)
	require.Equal(t, "Container", typeName)

	// Addressable types OUTSIDE the capability allowlist do not — Directory
	// falls back to host paths, Secret mints from env://-style URIs (see
	// liftableTypes; each admission needs its own capability review).
	_, ok = liftableObjectArg(arg("withDir", "dir"))
	require.False(t, ok)
	_, ok = liftableObjectArg(arg("importDir", "dir"))
	require.False(t, ok)
	_, ok = liftableObjectArg(arg("withToken", "token"))
	require.False(t, ok)

	// Non-addressable object types do not.
	_, ok = liftableObjectArg(arg("apply", "changes"))
	require.False(t, ok)

	// Nor do plain scalars, or LISTS of liftable objects.
	_, ok = liftableObjectArg(arg("exec", "cmd"))
	require.False(t, ok)
	_, ok = liftableObjectArg(arg("execAll", "sandboxes"))
	require.False(t, ok)
}

func TestArgTypeToJSONSchema(t *testing.T) {
	schema := objectToolsTestSchema(t)

	// An `ID` scalar (object handle) renders as a plain string.
	idType := &ast.Type{NamedType: "ID", NonNull: true}
	got, err := argTypeToJSONSchema(schema, idType)
	require.NoError(t, err)
	require.Equal(t, "string", got["type"])

	// A nested list of scalars recurses.
	listType := &ast.Type{Elem: &ast.Type{NamedType: "String", NonNull: true}, NonNull: true}
	got, err = argTypeToJSONSchema(schema, listType)
	require.NoError(t, err)
	require.Equal(t, "array", got["type"])
	require.Equal(t, "string", got["items"].(map[string]any)["type"])
}

// TestCombineSpanResult covers the combined result's contract: the target's
// own output and the trace report are BOTH carried, in that order, with the
// output under its own "== OUTPUT ==" heading, the report unlabelled (its own
// sections are already headed), no empty sections and a closing ReadLogs
// breadcrumb. A subtree that renders to nothing yields "" so the caller falls
// back to the flat captured logs (never an empty tool result).
func TestCombineSpanResult(t *testing.T) {
	const spanID = "00000000000000aa"

	// Renders to nothing: dagui filters internal/passthrough/encapsulated
	// spans, so a tool call with children can still produce a blank report.
	require.Empty(t, combineSpanResult(spanID, "", ""))
	require.Empty(t, combineSpanResult(spanID, "LINE-01", "\n \n\t\n"))

	// Report only: no empty OUTPUT section for a target that printed nothing,
	// and no heading over the report itself.
	quiet := combineSpanResult(spanID, "", "== CHECKS ==  ✔ 1 passed\n✔ lint:check 0.1s OK")
	require.NotContains(t, quiet, "OUTPUT")
	require.NotContains(t, quiet, "TRACE REPORT")
	require.True(t, strings.HasPrefix(quiet, "== CHECKS =="), "got %q", quiet)

	got := combineSpanResult(spanID, "LINE-01\nLINE-02", "• Foo.bar 1.0s")
	// The tool's own output comes first, verbatim, under its own heading...
	require.Contains(t, got, "== OUTPUT ==\nLINE-01\nLINE-02")
	// ...then the report, bare.
	require.Contains(t, got, "LINE-02\n\n• Foo.bar")
	require.NotContains(t, got, "TRACE REPORT")
	require.Less(t, strings.Index(got, "== OUTPUT =="), strings.Index(got, "• Foo.bar"))

	// The breadcrumb names the span, in the same vocabulary as the flat
	// path's "... N lines omitted (use ReadLogs(span: X) to read more)".
	require.Contains(t, got, "use ReadLogs(span: "+spanID+") to read the full logs")
	// ...and comes last, after the report's own trailing sections.
	lines := strings.Split(got, "\n")
	require.Contains(t, lines[len(lines)-1], "ReadLogs")
}

// TestDirectLogs covers the OUTPUT section's source: only the lines the
// captured span printed itself, in order, unabridged.
func TestDirectLogs(t *testing.T) {
	require.Empty(t, directLogs(nil))
	require.Empty(t, directLogs([]capturedLine{{text: "nested", direct: false}}))
	require.Equal(t, "a\nb", directLogs([]capturedLine{
		{text: "a", direct: true},
		{text: "nested", direct: false},
		{text: "b", direct: true},
	}))
}

// liftTestRunner is a minimal receiver type for exercising
// buildObjectMethodSelector's address lifting against a real dagql server.
type liftTestRunner struct{}

func (*liftTestRunner) Type() *ast.Type {
	return &ast.Type{NamedType: "LiftTestRunner", NonNull: true}
}

// newAddressLiftTestServer builds a dagql server with a miniature Address API
// — Query.address(value).container — mirroring core/schema/address.go, plus a
// LiftTestRunner receiver whose exec method takes a required Container arg
// and whose withDir method takes an optional Directory arg (addressable in
// the CLI, but outside the liftableTypes allowlist). The fake .container
// resolver records the address in the container's ImageRef, so tests can
// observe which address resolved, and fails for "bogus:ref" to exercise the
// both-attempts-failed error. No .directory field exists, so an (incorrect)
// lift attempt for a Directory arg would fail loudly rather than silently.
func newAddressLiftTestServer(t *testing.T) *dagql.Server {
	t.Helper()
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Container]{Typed: &Container{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{Typed: &Directory{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Address]{Typed: &Address{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*liftTestRunner]{Typed: &liftTestRunner{}}))
	dagql.Fields[*Query]{
		dagql.Func("address", func(_ context.Context, _ *Query, args struct {
			Value dagql.String
		}) (*Address, error) {
			return &Address{Value: args.Value.String()}, nil
		}),
		dagql.Func("runner", func(_ context.Context, _ *Query, _ struct{}) (*liftTestRunner, error) {
			return &liftTestRunner{}, nil
		}),
	}.Install(srv)
	dagql.Fields[*Address]{
		dagql.Func("container", func(_ context.Context, addr *Address, _ struct{}) (*Container, error) {
			if addr.Value == "bogus:ref" {
				return nil, fmt.Errorf("no such image %q", addr.Value)
			}
			return &Container{ImageRef: addr.Value}, nil
		}),
	}.Install(srv)
	dagql.Fields[*liftTestRunner]{
		dagql.Func("exec", func(ctx context.Context, _ *liftTestRunner, args struct {
			Cmd     dagql.String
			Sandbox dagql.ID[*Container]
		}) (dagql.String, error) {
			ctr, err := args.Sandbox.Load(ctx, srv)
			if err != nil {
				return "", err
			}
			return dagql.String(args.Cmd.String() + " in " + ctr.Self().ImageRef), nil
		}),
		dagql.Func("withDir", func(_ context.Context, r *liftTestRunner, _ struct {
			Dir dagql.Optional[dagql.ID[*Directory]]
		}) (*liftTestRunner, error) {
			return r, nil
		}),
	}.Install(srv)
	return srv
}

// TestBuildObjectMethodSelectorAddressLift covers dispatch: a model-supplied
// string for a liftable object arg first tries the ID decode (IDs from
// previous tool results keep working), then falls back to lifting the string
// through Query.address(value).<addressField>. Args of addressable types
// outside the liftableTypes allowlist only ever take the ID path.
func TestBuildObjectMethodSelectorAddressLift(t *testing.T) {
	// Select requires client metadata and a dagql cache in ctx (cache sessions
	// are per-client).
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "lift-test",
		SessionID: "lift-test",
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newAddressLiftTestServer(t)

	var runner dagql.AnyObjectResult
	require.NoError(t, srv.Select(ctx, srv.Root(), &runner, dagql.Selector{Field: "runner"}))

	execField := fieldByName(srv.Schema().Types["LiftTestRunner"], "exec")
	require.NotNil(t, execField)
	// The generated schema carries @expectedType for the ID-typed arg — this is
	// what liftableObjectArg keys off at dispatch time.
	typeName, ok := liftableObjectArg(execField.Arguments.ForName("sandbox"))
	require.True(t, ok)
	require.Equal(t, "Container", typeName)

	t.Run("address string lifts into the object", func(t *testing.T) {
		sel, err := buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": "alpine:latest",
		})
		require.NoError(t, err)
		var out dagql.String
		require.NoError(t, srv.Select(ctx, runner, &out, sel))
		require.Equal(t, "make in alpine:latest", out.String())
	})

	t.Run("a real ID still decodes directly", func(t *testing.T) {
		var ctr dagql.AnyObjectResult
		require.NoError(t, srv.Select(ctx, srv.Root(), &ctr,
			dagql.Selector{
				Field: "address",
				Args:  []dagql.NamedInput{{Name: "value", Value: dagql.String("premade")}},
			},
			dagql.Selector{Field: "container"},
		))
		ctrID, err := ctr.ID()
		require.NoError(t, err)
		encoded, err := ctrID.Encode()
		require.NoError(t, err)

		sel, err := buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": encoded,
		})
		require.NoError(t, err)
		// The ID passed through unchanged — no address() wrapping.
		var found bool
		for _, arg := range sel.Args {
			if arg.Name != "sandbox" {
				continue
			}
			found = true
			argID, err := arg.Value.(dagql.IDable).ID()
			require.NoError(t, err)
			reEncoded, err := argID.Encode()
			require.NoError(t, err)
			require.Equal(t, encoded, reEncoded)
		}
		require.True(t, found)
		var out dagql.String
		require.NoError(t, srv.Select(ctx, runner, &out, sel))
		require.Equal(t, "make in premade", out.String())
	})

	t.Run("unresolvable address reports both attempts", func(t *testing.T) {
		_, err := buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": "bogus:ref",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `"bogus:ref" is neither a Container ID`)
		require.Contains(t, err.Error(), "nor a resolvable Container address")
		require.Contains(t, err.Error(), "no such image")
	})

	t.Run("non-string values surface the plain decode error", func(t *testing.T) {
		_, err := buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": 42,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `arg "sandbox": decode int`)
	})

	t.Run("non-addressable args do not lift", func(t *testing.T) {
		// cmd is a plain String: a bad value errors without any address lookup.
		_, err := buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     42,
			"sandbox": "alpine:latest",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `arg "cmd"`)
	})

	t.Run("addressable-but-not-liftable args do not lift", func(t *testing.T) {
		// Directory is addressable in the CLI, but outside the liftableTypes
		// capability allowlist (its Address decoder falls back to HOST paths).
		// A plain string for a Directory arg therefore surfaces the ID decode
		// error with NO address lookup attempted — the test server's Address
		// has no .directory field, so an attempted lift would produce a
		// "neither ... nor a resolvable ... address" error instead.
		withDirField := fieldByName(srv.Schema().Types["LiftTestRunner"], "withDir")
		require.NotNil(t, withDirField)
		_, ok := liftableObjectArg(withDirField.Arguments.ForName("dir"))
		require.False(t, ok)

		_, err := buildObjectMethodSelector(ctx, srv, runner.ObjectType(), withDirField, map[string]any{
			"dir": "some/host/path",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `arg "dir": decode string`)
		require.NotContains(t, err.Error(), "address")
	})
}
