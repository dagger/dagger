package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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
type Agent { id: ID! }
type Container { id: ID! }
type Directory { id: ID! }
type File { id: ID! }
type GitRef { id: ID! }
type GitRepository { id: ID! }
type Service { id: ID! }
type Secret { id: ID! }
type Socket { id: ID! }
type Volume { id: ID! }
type Artifacts { id: ID! }
type Artifact { id: ID! }

enum Mode { FAST SLOW }
input Options {
  label: String
  mode: Mode
  values: [String]
}

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

  "Compact the current conversation — MCP supplies the LLM argument."
  compact(llm: ID! @expectedType(name: "LLM")): LLM!

  "Note the conversation, if there is one — an optional LLM argument."
  annotate(llm: ID @expectedType(name: "LLM")): Doug!

  "Poke the calling agent — MCP supplies the Agent argument."
  poke(
    caller: ID! @expectedType(name: "Agent"),
    note: String!,
  ): String!

  "Apply a changeset — requires a non-liftable object arg, so ineligible."
  apply(changes: ID! @expectedType(name: "Changeset")): Doug!

  "Run a command — its required Container arg is LIFTABLE, so eligible."
  exec(
    cmd: String!,
    sandbox: ID! @expectedType(name: "Container"),
  ): String!

  "Run everywhere — a required LIST of liftable object args lifts element-wise."
  execAll(
    cmd: String!,
    sandboxes: [ID!]! @expectedType(name: "Container"),
  ): String!

  "Authenticate everywhere — a required LIST of blocklisted object args."
  withTokens(tokens: [ID!]! @expectedType(name: "Secret")): Doug!

  "Debug in a sandbox — an optional liftable arg."
  debug(sandbox: ID @expectedType(name: "Container")): Doug!

  "Mount a directory — an optional liftable arg."
  withDir(dir: ID @expectedType(name: "Directory")): Doug!

  "Import a directory — a required liftable arg, so eligible."
  importDir(dir: ID! @expectedType(name: "Directory")): Doug!

  "Rebase onto a git ref — a required liftable arg, so eligible."
  rebase(onto: ID! @expectedType(name: "GitRef")): Doug!

  "Read one file — a required liftable arg, so eligible."
  cat(file: ID! @expectedType(name: "File")): String!

  "Clone a repository — a required liftable arg, so eligible."
  clone(repo: ID! @expectedType(name: "GitRepository")): Doug!

  "Probe a service — a required liftable arg, so eligible."
  probe(svc: ID! @expectedType(name: "Service")): String!

  "Authenticate — a required Secret arg, deliberately not liftable."
  withToken(token: ID! @expectedType(name: "Secret")): Doug!

  "Forward a socket — a required Socket arg, deliberately not liftable."
  withSocket(sock: ID! @expectedType(name: "Socket")): Doug!

  "Mount a volume — a required Volume arg, deliberately not liftable."
  withVolume(vol: ID! @expectedType(name: "Volume")): Doug!

  "Authenticate if asked — an optional Secret arg keeps the ID convention."
  maybeToken(token: ID @expectedType(name: "Secret")): Doug!

  "Run checks — a required artifact selection, lifted from a DAG address."
  check(targets: ID! @expectedType(name: "Artifacts")): String!

  "Evaluate one artifact — a required artifact, lifted from a DAG address."
  eval(target: ID! @expectedType(name: "Artifact")): String!

  "Run checks everywhere — a LIST of selections lifts element-wise."
  checkAll(targets: [ID!]! @expectedType(name: "Artifacts")): String!

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

func requireNullableJSONSchema(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	variants, ok := schema["anyOf"].([]any)
	require.True(t, ok, "nullable schema must use anyOf: %#v", schema)
	require.Len(t, variants, 2)
	nonNull, ok := variants[0].(map[string]any)
	require.True(t, ok)
	nullSchema, ok := variants[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "null", nullSchema["type"])
	return nonNull
}

func TestObjectToolEligible(t *testing.T) {
	schema := objectToolsTestSchema(t)
	doug := schema.Types["Doug"]

	// Methods whose required args are all scalars (or the auto-injected Workspace)
	// are eligible.
	require.True(t, objectToolEligible(fieldByName(doug, "read"), nil, conversationToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "write"), nil, conversationToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "todoWrite"), nil, conversationToolArgs))

	// A required object-typed argument disqualifies the method — the model has no
	// handle to pass.
	require.False(t, objectToolEligible(fieldByName(doug, "apply"), nil, conversationToolArgs))

	// LLM and Agent handles are supplied by MCP at object-tool dispatch, so
	// these required arguments do not disqualify the methods...
	require.True(t, objectToolEligible(fieldByName(doug, "compact"), nil, conversationToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "annotate"), nil, conversationToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "poke"), nil, conversationToolArgs))

	// ...unless the tools are served without a conversation to fill them from
	// (dagger mcp). Then an LLM or Agent argument is unsatisfiable like any
	// other object argument: a required one disqualifies the method, an
	// optional one is left to the caller.
	require.False(t, objectToolEligible(fieldByName(doug, "compact"), nil, standaloneToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "annotate"), nil, standaloneToolArgs))
	require.False(t, objectToolEligible(fieldByName(doug, "poke"), nil, standaloneToolArgs))

	// ...or when the type is LIFTABLE: a required Container arg renders as an
	// address string and is lifted via the core Address API at dispatch time.
	require.True(t, objectToolEligible(fieldByName(doug, "exec"), nil, conversationToolArgs))

	// A required LIST of liftable objects lifts element-wise, so it does
	// not disqualify either; a list of blocklisted ones still does.
	require.True(t, objectToolEligible(fieldByName(doug, "execAll"), nil, conversationToolArgs))
	require.False(t, objectToolEligible(fieldByName(doug, "withTokens"), nil, conversationToolArgs))

	// An optional object arg never disqualified; still doesn't — liftable or
	// not.
	require.True(t, objectToolEligible(fieldByName(doug, "debug"), nil, conversationToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "withDir"), nil, conversationToolArgs))

	// Every addressable type outside the capability blocklist lifts — a
	// Directory, File, GitRef, GitRepository or Service address only loads
	// content the model could name anyway — so a required arg of one of them
	// does not disqualify.
	for _, name := range []string{"importDir", "rebase", "cat", "clone", "probe"} {
		require.True(t, objectToolEligible(fieldByName(doug, name), nil, conversationToolArgs), name)
	}

	// Secret, Socket and Volume are blocklisted: their Address decoders MINT
	// capabilities from a string — env:// / file:// / op:// secrets, host
	// sockets, sshfs:// and engine volumes — so the model must be handed an
	// ID instead, and a required arg of one of them disqualifies.
	for _, name := range []string{"withToken", "withSocket", "withVolume"} {
		require.False(t, objectToolEligible(fieldByName(doug, name), nil, conversationToolArgs), name)
	}
	// An optional one is still a tool; it just takes an ID.
	require.True(t, objectToolEligible(fieldByName(doug, "maybeToken"), nil, conversationToolArgs))

	// A required artifact selection lifts from a DAG address into the
	// conversation's scope, whether it takes the selection or the one
	// artifact it selects, and so does a list of selections.
	require.True(t, objectToolEligible(fieldByName(doug, "check"), nil, conversationToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "eval"), nil, conversationToolArgs))
	require.True(t, objectToolEligible(fieldByName(doug, "checkAll"), nil, conversationToolArgs))

	// except drops a method by name.
	require.False(t, objectToolEligible(fieldByName(doug, "read"), []string{"read"}, conversationToolArgs))

	// Reserved / internal / deprecated fields are never tools.
	require.False(t, objectToolEligible(fieldByName(doug, "id"), nil, conversationToolArgs))
	require.False(t, objectToolEligible(fieldByName(doug, "sync"), nil, conversationToolArgs))
	require.False(t, objectToolEligible(fieldByName(doug, "old"), nil, conversationToolArgs))
}

func TestObjectMethodSchema(t *testing.T) {
	schema := objectToolsTestSchema(t)
	doug := schema.Types["Doug"]

	readSchema, err := objectMethodSchema(schema, fieldByName(doug, "read"), conversationToolArgs)
	require.NoError(t, err)
	props := readSchema["properties"].(map[string]any)

	// Workspace remains contextual, while LLM and Agent arguments are filled
	// directly by MCP. None are exposed to the model's tool schema.
	require.NotContains(t, props, "source")
	compactSchema, err := objectMethodSchema(schema, fieldByName(doug, "compact"), conversationToolArgs)
	require.NoError(t, err)
	require.NotContains(t, compactSchema["properties"], "llm")
	annotateSchema, err := objectMethodSchema(schema, fieldByName(doug, "annotate"), conversationToolArgs)
	require.NoError(t, err)
	require.NotContains(t, annotateSchema["properties"], "llm")
	pokeSchema, err := objectMethodSchema(schema, fieldByName(doug, "poke"), conversationToolArgs)
	require.NoError(t, err)
	require.NotContains(t, pokeSchema["properties"], "caller")
	require.Contains(t, pokeSchema["properties"], "note")

	// Served without a conversation, MCP has nothing to fill an LLM argument
	// from, so an optional one is exposed by ID like any other object.
	annotateSchema, err = objectMethodSchema(schema, fieldByName(doug, "annotate"), standaloneToolArgs)
	require.NoError(t, err)
	annotateLLM := annotateSchema["properties"].(map[string]any)["llm"].(map[string]any)
	require.Equal(t, "(LLM ID)", annotateLLM["description"])
	require.Equal(t, "string", requireNullableJSONSchema(t, annotateLLM)["type"])

	// Scalar args are surfaced with their JSON types; required tracks non-null
	// args without a default.
	require.Equal(t, "string", props["filePath"].(map[string]any)["type"])
	require.Equal(t, "integer", props["offset"].(map[string]any)["type"])
	require.EqualValues(t, 0, props["offset"].(map[string]any)["default"])
	date := props["date"].(map[string]any)
	require.Equal(t, "string", requireNullableJSONSchema(t, date)["type"])
	require.Contains(t, date, "default")
	require.Nil(t, date["default"])
	require.Equal(t, []string{"filePath"}, readSchema["required"])
	require.Equal(t, false, readSchema["additionalProperties"])

	// A list arg with a default is optional and rendered as an array of scalars.
	todoSchema, err := objectMethodSchema(schema, fieldByName(doug, "todoWrite"), conversationToolArgs)
	require.NoError(t, err)
	todoProps := todoSchema["properties"].(map[string]any)
	pending := todoProps["pending"].(map[string]any)
	require.Equal(t, "array", pending["type"])
	require.Equal(t, "string", pending["items"].(map[string]any)["type"])
	require.NotContains(t, todoSchema, "required") // pending has a default

	// A required liftable object arg renders as a string, described as an
	// address — with the type's own syntax hint from addressableTypes — and
	// is required.
	containerHint := "(Container address: " + addressableTypes["Container"].hint + ")"
	require.Contains(t, containerHint, `an image ref like "golang:1.26"`)
	require.Contains(t, containerHint, "dag://")
	require.Contains(t, containerHint, "or a Container ID from a prior tool result")
	execSchema, err := objectMethodSchema(schema, fieldByName(doug, "exec"), conversationToolArgs)
	require.NoError(t, err)
	execProps := execSchema["properties"].(map[string]any)
	sandbox := execProps["sandbox"].(map[string]any)
	require.Equal(t, "string", sandbox["type"])
	require.Equal(t, containerHint, sandbox["description"])
	require.ElementsMatch(t, []string{"cmd", "sandbox"}, execSchema["required"])

	// An optional liftable object arg says "address" too — dispatch lifts
	// both.
	debugSchema, err := objectMethodSchema(schema, fieldByName(doug, "debug"), conversationToolArgs)
	require.NoError(t, err)
	debugProperty := debugSchema["properties"].(map[string]any)["sandbox"].(map[string]any)
	debug := requireNullableJSONSchema(t, debugProperty)
	require.Equal(t, "string", debug["type"])
	require.Equal(t, containerHint, debugProperty["description"])

	// Every liftable type carries its own hint: its accepted external syntax,
	// the dag:// form, and the ID fallback. Forms that would reach the
	// calling client's host are never advertised.
	for field, want := range map[string]struct{ arg, typeName, external string }{
		"withDir": {"dir", "Directory", `"https://github.com/org/repo#main:docs"`},
		"cat":     {"file", "File", `"https://github.com/org/repo#main:README.md"`},
		"rebase":  {"onto", "GitRef", `"https://github.com/org/repo#main"`},
		"clone":   {"repo", "GitRepository", `"https://github.com/org/repo"`},
		"probe":   {"svc", "Service", "dag://"},
	} {
		s, err := objectMethodSchema(schema, fieldByName(doug, field), conversationToolArgs)
		require.NoError(t, err)
		desc := s["properties"].(map[string]any)[want.arg].(map[string]any)["description"].(string)
		require.True(t, strings.HasPrefix(desc, "("+want.typeName+" address: "), desc)
		require.Contains(t, desc, want.external)
		require.Contains(t, desc, "dag://<module>/")
		require.Contains(t, desc, "FindArtifacts lists what exists")
		require.Contains(t, desc, "or a "+want.typeName+" ID from a prior tool result")
		for _, hostForm := range []string{"tcp://", "udp://", "ssh://", "file://", "local"} {
			require.NotContains(t, desc, hostForm, field)
		}
	}

	// A blocklisted type keeps the ID convention: the model may hand back an
	// ID from a prior tool result, but is not invited to write an address
	// (dispatch would refuse it anyway).
	tokenSchema, err := objectMethodSchema(schema, fieldByName(doug, "maybeToken"), conversationToolArgs)
	require.NoError(t, err)
	tokenProperty := tokenSchema["properties"].(map[string]any)["token"].(map[string]any)
	require.Equal(t, "string", requireNullableJSONSchema(t, tokenProperty)["type"])
	require.Equal(t, "(Secret ID)", tokenProperty["description"])

	// A non-addressable object arg keeps the ID convention.
	applySchema, err := objectMethodSchema(schema, fieldByName(doug, "apply"), conversationToolArgs)
	require.NoError(t, err)
	changes := applySchema["properties"].(map[string]any)["changes"].(map[string]any)
	require.Equal(t, "(Changeset ID)", changes["description"])

	// A LIST of liftable objects is an array of address strings, described
	// as such with the element type's hint.
	execAllSchema, err := objectMethodSchema(schema, fieldByName(doug, "execAll"), conversationToolArgs)
	require.NoError(t, err)
	sandboxes := execAllSchema["properties"].(map[string]any)["sandboxes"].(map[string]any)
	require.Equal(t, "array", sandboxes["type"])
	require.Equal(t, "string", sandboxes["items"].(map[string]any)["type"])
	require.Equal(t, "(list of Container addresses, each "+addressableTypes["Container"].hint+")", sandboxes["description"])
	require.ElementsMatch(t, []string{"cmd", "sandboxes"}, execAllSchema["required"])

	// A list of blocklisted objects keeps the ID convention.
	withTokensSchema, err := objectMethodSchema(schema, fieldByName(doug, "withTokens"), conversationToolArgs)
	require.NoError(t, err)
	tokens := withTokensSchema["properties"].(map[string]any)["tokens"].(map[string]any)
	require.Equal(t, "array", tokens["type"])
	require.Equal(t, "(Secret ID)", tokens["description"])

	// Artifact selections take a DAG address, scheme optional, and the hint
	// teaches its vocabulary: path globs, dimension keys, type assertions,
	// and where to find what exists. An Artifact must select one.
	for field, want := range map[string]struct{ arg, typeName string }{
		"check": {"targets", "Artifacts"},
		"eval":  {"target", "Artifact"},
	} {
		s, err := objectMethodSchema(schema, fieldByName(doug, field), conversationToolArgs)
		require.NoError(t, err)
		prop := s["properties"].(map[string]any)[want.arg].(map[string]any)
		require.Equal(t, "string", prop["type"])
		desc := prop["description"].(string)
		require.True(t, strings.HasPrefix(desc, "("+want.typeName+" address: "), desc)
		for _, vocabulary := range []string{`"dag://" scheme is optional`, `"go/**"`, `?<dimension>=<key>`, `"go/test?go-test=TestX"`, `dag+<type>://`, "FindArtifacts lists what exists", "or an " + want.typeName + " ID from a prior tool result"} {
			require.Contains(t, strings.ToLower(desc), strings.ToLower(vocabulary), field)
		}
		require.Equal(t, []string{want.arg}, s["required"])
	}
	evalSchema, err := objectMethodSchema(schema, fieldByName(doug, "eval"), conversationToolArgs)
	require.NoError(t, err)
	require.Contains(t, evalSchema["properties"].(map[string]any)["target"].(map[string]any)["description"], "must select exactly one artifact")
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

	// Every addressable type outside the blocklist qualifies...
	for field, want := range map[string]struct{ arg, typeName string }{
		"withDir":   {"dir", "Directory"},
		"importDir": {"dir", "Directory"},
		"cat":       {"file", "File"},
		"rebase":    {"onto", "GitRef"},
		"clone":     {"repo", "GitRepository"},
		"probe":     {"svc", "Service"},
	} {
		typeName, ok := liftableObjectArg(arg(field, want.arg))
		require.True(t, ok, field)
		require.Equal(t, want.typeName, typeName)
	}

	// ...but the blocklisted ones do not: Secret mints from env://-style
	// URIs, Socket forwards host sockets, Volume mounts sshfs:// or engine
	// volumes (see unliftableTypes).
	_, ok = liftableObjectArg(arg("withToken", "token"))
	require.False(t, ok)
	_, ok = liftableObjectArg(arg("maybeToken", "token"))
	require.False(t, ok)
	_, ok = liftableObjectArg(arg("withSocket", "sock"))
	require.False(t, ok)
	_, ok = liftableObjectArg(arg("withVolume", "vol"))
	require.False(t, ok)

	// Every addressable type has a loader field, and every liftable one a
	// hint; the blocklist only names addressable types.
	for name, typ := range addressableTypes {
		require.NotEmpty(t, typ.addressField, name)
		if !unliftableTypes[name] {
			require.NotEmpty(t, typ.hint, name)
		}
	}
	for name := range unliftableTypes {
		require.Contains(t, addressableTypes, name)
	}

	// Non-addressable object types do not.
	_, ok = liftableObjectArg(arg("apply", "changes"))
	require.False(t, ok)

	// Artifact selections lift, singly or as a list.
	typeName, ok = liftableObjectArg(arg("check", "targets"))
	require.True(t, ok)
	require.Equal(t, "Artifacts", typeName)
	typeName, ok = liftableObjectArg(arg("eval", "target"))
	require.True(t, ok)
	require.Equal(t, "Artifact", typeName)
	typeName, ok = liftableObjectArg(arg("checkAll", "targets"))
	require.True(t, ok)
	require.Equal(t, "Artifacts", typeName)

	// LISTS of liftable objects lift element-wise; lists of blocklisted ones
	// do not.
	typeName, ok = liftableObjectArg(arg("execAll", "sandboxes"))
	require.True(t, ok)
	require.Equal(t, "Container", typeName)
	_, ok = liftableObjectArg(arg("withTokens", "tokens"))
	require.False(t, ok)

	// Nor do plain scalars.
	_, ok = liftableObjectArg(arg("exec", "cmd"))
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

	// Nullable wrappers apply at every GraphQL type boundary, including list
	// elements, enums, and fields nested in input objects.
	nullableList := &ast.Type{Elem: &ast.Type{NamedType: "String"}}
	got, err = argTypeToJSONSchema(schema, nullableList)
	require.NoError(t, err)
	listSchema := requireNullableJSONSchema(t, got)
	require.Equal(t, "array", listSchema["type"])
	require.Equal(t, "string", requireNullableJSONSchema(t,
		listSchema["items"].(map[string]any))["type"])

	got, err = argTypeToJSONSchema(schema, &ast.Type{NamedType: "Mode"})
	require.NoError(t, err)
	enumSchema := requireNullableJSONSchema(t, got)
	require.Equal(t, "string", enumSchema["type"])
	require.Equal(t, []string{"FAST", "SLOW"}, enumSchema["enum"])

	got, err = argTypeToJSONSchema(schema, &ast.Type{NamedType: "Options"})
	require.NoError(t, err)
	inputSchema := requireNullableJSONSchema(t, got)
	require.Equal(t, "object", inputSchema["type"])
	inputProps := inputSchema["properties"].(map[string]any)
	require.Equal(t, "string", requireNullableJSONSchema(t,
		inputProps["label"].(map[string]any))["type"])
	require.Equal(t, "string", requireNullableJSONSchema(t,
		inputProps["mode"].(map[string]any))["type"])
	nestedList := requireNullableJSONSchema(t, inputProps["values"].(map[string]any))
	require.Equal(t, "string", requireNullableJSONSchema(t,
		nestedList["items"].(map[string]any))["type"])
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
	require.Empty(t, combineSpanResult(spanID, "", "", ""))
	require.Empty(t, combineSpanResult(spanID, "LINE-01", "\n \n\t\n", ""))

	// Report only: no empty OUTPUT section for a target that printed nothing.
	quiet := combineSpanResult(spanID, "", "== CHECKS ==  ✔ 1 passed\n✔ lint:check 0.1s OK", "")
	require.NotContains(t, quiet, "OUTPUT")
	require.True(t, strings.HasPrefix(quiet, "== CHECKS =="), "got %q", quiet)

	got := combineSpanResult(spanID, "LINE-01\nLINE-02", "• Foo.bar 1.0s", "")
	// The tool's own output comes first, verbatim, under its own heading...
	require.Contains(t, got, "== OUTPUT ==\nLINE-01\nLINE-02")
	// ...then the report, bare.
	require.Contains(t, got, "LINE-02\n\n• Foo.bar")
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

// TestSpanResultOutputKeepsNestedTail covers a tool call whose report body is
// just surfaced sections (here only SERVICES, because a service started
// beneath the call): with the span tree hidden, OUTPUT is the only place the
// nested output can land, so it must keep the flat path's shape -- direct
// lines verbatim, nested lines abridged to a tail, never dropped.
func TestSpanResultOutputKeepsNestedTail(t *testing.T) {
	const spanID = "00000000000000aa"

	var lines []capturedLine
	// Nested output from an early exec that falls entirely outside the tail.
	lines = append(lines, capturedLine{text: "EARLY"})
	lines = append(lines, capturedLine{text: "REPORT-START", direct: true})
	nested := llmToolLogsMaxLines + 4
	for i := 1; i <= nested; i++ {
		lines = append(lines, capturedLine{text: fmt.Sprintf("EXEC-%02d", i)})
	}
	lines = append(lines, capturedLine{text: "REPORT-END", direct: true})

	opts := toolCallReportOpts()
	require.True(t, opts.HideSpanTree)
	own := spanResultOutput(spanID, lines, opts.HideSpanTree)
	got := combineSpanResult(spanID, own, "== SERVICES ==\n● svc running", "")

	// Direct lines stay verbatim; each abridged nested run is counted, not
	// silently dropped.
	require.Contains(t, got, "== OUTPUT ==\n... 1 lines omitted (use ReadLogs(span: "+spanID+") to read more) ...\nREPORT-START\n"+
		"... 4 lines omitted (use ReadLogs(span: "+spanID+") to read more) ...\nEXEC-05\n")
	require.Contains(t, got, "EXEC-12\nREPORT-END\n\n== SERVICES ==")
	// The nested tail survives.
	for i := nested - llmToolLogsMaxLines + 1; i <= nested; i++ {
		require.Contains(t, got, fmt.Sprintf("EXEC-%02d", i))
	}
	for i := 1; i <= nested-llmToolLogsMaxLines; i++ {
		require.NotContains(t, got, fmt.Sprintf("EXEC-%02d", i))
	}
	require.NotContains(t, got, "EARLY")

	// With the span tree shown (ReadTrace), nested logs render in the tree
	// under their own rows: OUTPUT stays direct-only.
	readOpts := readTraceReportOpts()
	require.False(t, readOpts.HideSpanTree)
	require.Equal(t, "REPORT-START\nREPORT-END", spanResultOutput(spanID, lines, readOpts.HideSpanTree))
}

// liftTestRunner is a minimal receiver type for exercising
// buildObjectMethodSelector's address lifting against a real dagql server.
type liftTestRunner struct{}

func (*liftTestRunner) Type() *ast.Type {
	return &ast.Type{NamedType: "LiftTestRunner", NonNull: true}
}

// newAddressLiftTestServer builds a dagql server with a miniature Address API
// — Query.address(value).container — mirroring core/schema/address.go, plus a
// LiftTestRunner receiver whose exec method takes a required Container arg,
// whose withDir method takes an optional (liftable) Directory arg and whose
// withToken method takes an optional (blocklisted) Secret arg. The fake
// .container resolver records the address in the container's ImageRef, so
// tests can observe which address resolved, and fails for "bogus:ref" to
// exercise the both-attempts-failed error. Like the real loader it takes
// noLock, and fails without it: lifting must resolve addresses live. No other
// loader exists, so a lift attempt for a Directory arg fails loudly as a
// failed address resolution, and one for a Secret arg would too.
func newAddressLiftTestServer(t *testing.T) *dagql.Server {
	t.Helper()
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Container]{Typed: &Container{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{Typed: &Directory{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Secret]{Typed: &Secret{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Address]{Typed: &Address{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*liftTestRunner]{Typed: &liftTestRunner{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*LLM]{Typed: &LLM{}}))
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
		dagql.Func("container", func(_ context.Context, addr *Address, args struct {
			NoLock bool `name:"noLock" default:"false"`
		}) (*Container, error) {
			if !args.NoLock {
				return nil, fmt.Errorf("lifted address %q must resolve with noLock", addr.Value)
			}
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
		dagql.Func("withToken", func(_ context.Context, r *liftTestRunner, _ struct {
			Token dagql.Optional[dagql.ID[*Secret]]
		}) (*liftTestRunner, error) {
			return r, nil
		}),
		dagql.Func("nullable", func(_ context.Context, _ *liftTestRunner, args struct {
			Date dagql.Optional[dagql.String]
		}) (dagql.String, error) {
			if !args.Date.Valid {
				return "null", nil
			}
			return args.Date.Value, nil
		}),
		// compact requires the calling conversation; annotate merely accepts
		// one. See TestStandaloneToolsTreatLLMArgsAsUnsatisfiable.
		dagql.Func("compact", func(_ context.Context, _ *liftTestRunner, _ struct {
			LLM dagql.ID[*LLM]
		}) (dagql.String, error) {
			return "compacted", nil
		}),
		dagql.Func("annotate", func(_ context.Context, _ *liftTestRunner, args struct {
			LLM dagql.Optional[dagql.ID[*LLM]]
		}) (dagql.String, error) {
			if !args.LLM.Valid {
				return "no conversation", nil
			}
			return "conversation", nil
		}),
	}.Install(srv)
	return srv
}

// TestStandaloneToolsTreatLLMArgsAsUnsatisfiable covers the tools an MCP serves
// without a conversation to drive them (dagger mcp): an LLM argument then has
// nothing to be filled from, so a method that requires one is not offered, and
// an optional one is simply left unset.
func TestStandaloneToolsTreatLLMArgsAsUnsatisfiable(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "standalone-test",
		SessionID: "standalone-test",
	})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newAddressLiftTestServer(t)
	var runner dagql.AnyObjectResult
	require.NoError(t, srv.Select(ctx, srv.Root(), &runner, dagql.Selector{Field: "runner"}))

	toolNames := func(t *testing.T, m *MCP) []string {
		t.Helper()
		toolsets, err := m.boundToolsets(srv)
		require.NoError(t, err)
		require.Len(t, toolsets, 1)
		names := make([]string, 0, len(toolsets[0].tools))
		for _, tool := range toolsets[0].tools {
			names = append(names, tool.Name)
		}
		return names
	}

	conversation := newMCP().WithTools(runner, srv.Schema(), nil)
	require.ElementsMatch(t, []string{"annotate", "compact", "exec", "nullable", "withDir", "withToken"}, toolNames(t, conversation))

	// Standalone, the method that REQUIRES a conversation is not offered...
	standalone := conversation.Standalone()
	require.ElementsMatch(t, []string{"annotate", "exec", "nullable", "withDir", "withToken"}, toolNames(t, standalone))
	// ...and survives cloning, which every binding does.
	require.ElementsMatch(t, []string{"annotate", "exec", "nullable", "withDir", "withToken"}, toolNames(t, standalone.Clone()))

	// ...while the optional argument is left unset, so the method sees null.
	annotateField := fieldByName(srv.Schema().Types["LiftTestRunner"], "annotate")
	require.NotNil(t, annotateField)
	sel, err := standalone.buildObjectMethodSelector(ctx, srv, runner.ObjectType(), annotateField, map[string]any{})
	require.NoError(t, err)
	var out dagql.String
	require.NoError(t, srv.Select(ctx, runner, &out, sel))
	require.Equal(t, "no conversation", out.String())

	// Driven by a conversation, the argument is MCP's to fill: a missing
	// dispatching conversation is then a bug, not a silent null.
	_, err = conversation.buildObjectMethodSelector(ctx, srv, runner.ObjectType(), annotateField, map[string]any{})
	require.ErrorContains(t, err, "requires the current conversation")
}

func TestLazyToolReplacementDoesNotLoadSupersededObject(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, sameSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("warm=%t/sameSession=%t", warm, sameSession), func(t *testing.T) {
				ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "source", SessionID: "source"})
				cache, err := dagql.NewCache(ctx, "", nil, nil)
				require.NoError(t, err)
				ctx = dagql.ContextWithCache(ctx, cache)
				srv := newAddressLiftTestServer(t)
				calls := map[string]int{}
				failA := false
				dagql.Fields[*Query]{
					dagql.Func("replacementRunner", func(_ context.Context, _ *Query, args struct{ Name string }) (*liftTestRunner, error) {
						calls[args.Name]++
						if args.Name == "A" && failA {
							return nil, fmt.Errorf("superseded A must not be loaded")
						}
						return &liftTestRunner{}, nil
					}),
				}.Install(srv)
				idFor := func(name string) *call.ID {
					return call.New().Append((&liftTestRunner{}).Type(), "replacementRunner",
						call.WithArgs(call.NewArgument("name", call.NewLiteralString(name), false)))
				}
				if warm {
					_, err := srv.Load(ctx, idFor("A"))
					require.NoError(t, err)
				}
				calls = map[string]int{}
				failA = true
				if !sameSession {
					ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "destination", SessionID: "destination"})
				}
				objType, ok := srv.ObjectType("LiftTestRunner")
				require.True(t, ok)
				mcp := newMCP().withLazyToolsOwner(idFor("A"), objType, srv.Schema(), nil, "owner-A", 1).
					withLazyToolsOwner(idFor("B"), objType, srv.Schema(), nil, "owner-B", 2)
				require.Len(t, mcp.boundTools, 1)
				require.Equal(t, "owner-B", mcp.boundTools[0].Owner)
				require.Equal(t, 2, mcp.boundTools[0].Version)
				toolsets, err := mcp.boundToolsets(srv)
				require.NoError(t, err)
				require.Empty(t, calls, "listing must not load either object")
				require.Len(t, toolsets, 1)
				invoked := false
				for _, tool := range toolsets[0].tools {
					if tool.Name != "nullable" {
						continue
					}
					out, err := tool.Call(ctx, map[string]any{"date": "B is active"})
					require.NoError(t, err)
					require.Contains(t, fmt.Sprint(out), "B is active")
					invoked = true
				}
				require.True(t, invoked)
				require.Equal(t, map[string]int{"B": 1}, calls)
			})
		}
	}
}

// TestBoundToolsUseTheirDefiningSchemaAuthoritatively covers both lazy bindings
// restored from IDs and eager bindings created by workspace module discovery.
// Even when the current workspace schema has a valid replacement definition for
// the same type, the binding must keep the methods from the schema it was
// composed with. The eager method call also proves dispatch remains callable
// through the captured receiver after the workspace schema changes.
func TestBoundToolsUseTheirDefiningSchemaAuthoritatively(t *testing.T) {
	defining := newAddressLiftTestServer(t)
	objType, ok := defining.ObjectType("LiftTestRunner")
	require.True(t, ok)

	// Install a different, valid definition of the same type in the current
	// schema. Treating definingSchema as a missing-type fallback would silently
	// replace the active tools with this method.
	current := newCoreDagqlServerForTest(t, &Query{})
	current.InstallObject(dagql.NewClass(current, dagql.ClassOpts[*liftTestRunner]{Typed: &liftTestRunner{}}))
	dagql.Fields[*liftTestRunner]{
		dagql.Func("replacement", func(_ context.Context, _ *liftTestRunner, _ struct{}) (dagql.String, error) {
			return "replacement", nil
		}),
	}.Install(current)

	assertDefiningTools := func(t *testing.T, mcp *MCP) []LLMTool {
		t.Helper()
		toolsets, err := mcp.boundToolsets(current)
		require.NoError(t, err)
		require.Len(t, toolsets, 1)
		require.Equal(t, "LiftTestRunner", toolsets[0].typeName)

		names := make([]string, 0, len(toolsets[0].tools))
		for _, tool := range toolsets[0].tools {
			names = append(names, tool.Name)
		}
		require.ElementsMatch(t, []string{"annotate", "compact", "exec", "nullable", "withDir", "withToken"}, names)
		require.NotContains(t, names, "replacement")
		return toolsets[0].tools
	}

	t.Run("lazy", func(t *testing.T) {
		assertDefiningTools(t, newMCP().WithLazyTools(nil, objType, defining.Schema(), nil))
	})

	t.Run("dispatch", func(t *testing.T) {
		ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
			ClientID:  "defining-schema-test",
			SessionID: "defining-schema-test",
		})
		cache, err := dagql.NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		ctx = dagql.ContextWithCache(ctx, cache)

		var runner dagql.AnyObjectResult
		require.NoError(t, defining.Select(ctx, defining.Root(), &runner, dagql.Selector{Field: "runner"}))
		for _, boundary := range []string{"eager", "lazy load", "state return", "dependency attachment"} {
			t.Run(boundary, func(t *testing.T) {
				mcp := newMCP().WithTools(runner, defining.Schema(), nil)
				currentType, ok := current.ObjectType("LiftTestRunner")
				require.True(t, ok)
				switch boundary {
				case "lazy load":
					id, err := runner.ID()
					require.NoError(t, err)
					mcp = newMCP().WithLazyTools(id, objType, defining.Schema(), nil)
				case "state return":
					returned, err := currentType.New(runner)
					require.NoError(t, err)
					require.NoError(t, mcp.rebindBoundTool("LiftTestRunner", returned))
				case "dependency attachment":
					llm := &LLM{mcp: mcp}
					deps, err := llm.AttachDependencyResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
						return currentType.New(res)
					})
					require.NoError(t, err)
					require.Len(t, deps, 1)
					dep, ok := deps[0].(dagql.AnyObjectResult)
					require.True(t, ok)
					_, ok = dep.ObjectType().FieldSpec("nullable", "")
					require.True(t, ok)
					_, ok = dep.ObjectType().FieldSpec("replacement", "")
					require.False(t, ok)
					out, err := dep.Select(ctx, current, dagql.Selector{
						Field: "nullable",
						Args:  []dagql.NamedInput{{Name: "date", Value: dagql.Opt(dagql.String("returned dependency"))}},
					})
					require.NoError(t, err)
					require.Equal(t, dagql.String("returned dependency"), out.Unwrap())
				}
				tools := assertDefiningTools(t, mcp)
				for _, tool := range tools {
					if tool.Name != "nullable" {
						continue
					}
					out, err := tool.Call(ctx, map[string]any{"date": "still active"})
					require.NoError(t, err)
					require.Equal(t, "still active", out)
					return
				}
				t.Fatal("nullable tool not found")
			})
		}
	})
}

// TestBuildObjectMethodSelector covers argument dispatch against a
// real dagql field: nullable scalars accept explicit null, while model-supplied
// strings for liftable object args first try ID decoding and then address
// resolution. Args of blocklisted types (unliftableTypes) only ever take the
// ID path.
func TestBuildObjectMethodSelector(t *testing.T) {
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
	dagql.Fields[*liftTestRunner]{
		dagql.Func("execAll", func(ctx context.Context, _ *liftTestRunner, args struct {
			Cmd       dagql.String
			Sandboxes dagql.ArrayInput[dagql.ID[*Container]]
		}) (dagql.String, error) {
			refs := make([]string, 0, len(args.Sandboxes))
			for _, id := range args.Sandboxes {
				ctr, err := id.Load(ctx, srv)
				if err != nil {
					return "", err
				}
				refs = append(refs, ctr.Self().ImageRef)
			}
			return dagql.String(args.Cmd.String() + " in " + strings.Join(refs, ", ")), nil
		}),
	}.Install(srv)

	var runner dagql.AnyObjectResult
	require.NoError(t, srv.Select(ctx, srv.Root(), &runner, dagql.Selector{Field: "runner"}))

	execField := fieldByName(srv.Schema().Types["LiftTestRunner"], "exec")
	require.NotNil(t, execField)
	// The generated schema carries @expectedType for the ID-typed arg — this is
	// what liftableObjectArg keys off at dispatch time.
	typeName, ok := liftableObjectArg(execField.Arguments.ForName("sandbox"))
	require.True(t, ok)
	require.Equal(t, "Container", typeName)

	execAllField := fieldByName(srv.Schema().Types["LiftTestRunner"], "execAll")
	require.NotNil(t, execAllField)
	// ...and for a list of IDs too.
	typeName, ok = liftableObjectArg(execAllField.Arguments.ForName("sandboxes"))
	require.True(t, ok)
	require.Equal(t, "Container", typeName)

	premadeID := func(t *testing.T, ref string) string {
		t.Helper()
		var ctr dagql.AnyObjectResult
		require.NoError(t, srv.Select(ctx, srv.Root(), &ctr,
			dagql.Selector{
				Field: "address",
				Args:  []dagql.NamedInput{{Name: "value", Value: dagql.String(ref)}},
			},
			dagql.Selector{Field: "container", Args: []dagql.NamedInput{{Name: "noLock", Value: dagql.Boolean(true)}}},
		))
		ctrID, err := ctr.ID()
		require.NoError(t, err)
		encoded, err := ctrID.Encode()
		require.NoError(t, err)
		return encoded
	}

	t.Run("list of addresses lifts element-wise", func(t *testing.T) {
		// Addresses and IDs from prior tool results mix freely: each element
		// that is not an ID is lifted on its own.
		sel, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execAllField, map[string]any{
			"cmd":       "make",
			"sandboxes": []any{"alpine:latest", premadeID(t, "premade"), "golang:1.26"},
		})
		require.NoError(t, err)
		var out dagql.String
		require.NoError(t, srv.Select(ctx, runner, &out, sel))
		require.Equal(t, "make in alpine:latest, premade, golang:1.26", out.String())
	})

	t.Run("list of IDs still decodes directly", func(t *testing.T) {
		sel, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execAllField, map[string]any{
			"cmd":       "make",
			"sandboxes": []any{premadeID(t, "one"), premadeID(t, "two")},
		})
		require.NoError(t, err)
		var out dagql.String
		require.NoError(t, srv.Select(ctx, runner, &out, sel))
		require.Equal(t, "make in one, two", out.String())
	})

	t.Run("list element errors name the element", func(t *testing.T) {
		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execAllField, map[string]any{
			"cmd":       "make",
			"sandboxes": []any{"alpine:latest", "bogus:ref"},
		})
		require.ErrorContains(t, err, `arg "sandboxes": element 1: "bogus:ref" is neither a Container ID`)
		require.ErrorContains(t, err, "no such image")

		// Elements are vetted like a single arg: nothing reaches the host.
		_, err = newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execAllField, map[string]any{
			"cmd":       "make",
			"sandboxes": []any{"dag://runner/sandbox"},
		})
		require.ErrorContains(t, err, `arg "sandboxes": element 0: "dag://runner/sandbox" is not a resolvable Container address`)

		// A non-string element surfaces the plain decode error.
		_, err = newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execAllField, map[string]any{
			"cmd":       "make",
			"sandboxes": []any{"alpine:latest", 42},
		})
		require.ErrorContains(t, err, `arg "sandboxes": decode []interface {}`)
	})

	t.Run("explicit null decodes for a nullable argument", func(t *testing.T) {
		nullableField := fieldByName(srv.Schema().Types["LiftTestRunner"], "nullable")
		require.NotNil(t, nullableField)
		sel, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), nullableField, map[string]any{
			"date": nil,
		})
		require.NoError(t, err)
		var out dagql.String
		require.NoError(t, srv.Select(ctx, runner, &out, sel))
		require.Equal(t, "null", out.String())
	})

	t.Run("address string lifts into the object", func(t *testing.T) {
		sel, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": "alpine:latest",
		})
		require.NoError(t, err)
		var out dagql.String
		require.NoError(t, srv.Select(ctx, runner, &out, sel))
		require.Equal(t, "make in alpine:latest", out.String())
	})

	t.Run("relative dag addresses need a conversation", func(t *testing.T) {
		// A relative DAG address resolves in the conversation's scope
		// (LLM.artifacts). Without a conversation there is none, and it is
		// refused before any Address loader runs: this server has no
		// Workspace.resolve, where the git decoders once read a DAG address
		// as a host path.
		m := newMCP().WithTools(runner, srv.Schema(), nil)
		_, err := m.buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": "dag://runner/sandbox",
		})
		require.ErrorContains(t, err, `"dag://runner/sandbox" is not a resolvable Container address: resolve "dag://runner/sandbox": no conversation to resolve the address in; FindArtifacts lists what exists`)
	})

	t.Run("absolute dag addresses need a bound workspace", func(t *testing.T) {
		// An absolute address names a workspace, not the conversation's
		// scope: it resolves in the conversation's bound workspace only,
		// never the calling client's current one. With none bound, it is
		// refused before any Address loader runs.
		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": "dag://github.com/org/repo@1111111111111111111111111111111111111111:runner/sandbox",
		})
		require.ErrorContains(t, err, "an absolute dag:// address names a workspace, and none is bound to this conversation")
	})

	t.Run("malformed dag addresses are reported", func(t *testing.T) {
		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": "dag://runner/sandbox?member=%zz",
		})
		require.ErrorContains(t, err, `"dag://runner/sandbox?member=%zz" is not a resolvable Container address`)
		require.ErrorContains(t, err, "invalid URL escape")
	})

	t.Run("a real ID still decodes directly", func(t *testing.T) {
		var ctr dagql.AnyObjectResult
		require.NoError(t, srv.Select(ctx, srv.Root(), &ctr,
			dagql.Selector{
				Field: "address",
				Args:  []dagql.NamedInput{{Name: "value", Value: dagql.String("premade")}},
			},
			dagql.Selector{Field: "container", Args: []dagql.NamedInput{{Name: "noLock", Value: dagql.Boolean(true)}}},
		))
		ctrID, err := ctr.ID()
		require.NoError(t, err)
		encoded, err := ctrID.Encode()
		require.NoError(t, err)

		sel, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
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
		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": "bogus:ref",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `"bogus:ref" is neither a Container ID`)
		require.Contains(t, err.Error(), "nor a resolvable Container address")
		require.Contains(t, err.Error(), "no such image")
	})

	t.Run("non-string values surface the plain decode error", func(t *testing.T) {
		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     "make",
			"sandbox": 42,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `arg "sandbox": decode int`)
	})

	t.Run("non-addressable args do not lift", func(t *testing.T) {
		// cmd is a plain String: a bad value errors without any address lookup.
		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), execField, map[string]any{
			"cmd":     42,
			"sandbox": "alpine:latest",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `arg "cmd"`)
	})

	t.Run("liftable args attempt the address", func(t *testing.T) {
		// Directory is liftable, so a plain string for a Directory arg is
		// resolved through Address.directory — which this test server lacks,
		// so the attempt surfaces as a failed address resolution.
		withDirField := fieldByName(srv.Schema().Types["LiftTestRunner"], "withDir")
		require.NotNil(t, withDirField)
		typeName, ok := liftableObjectArg(withDirField.Arguments.ForName("dir"))
		require.True(t, ok)
		require.Equal(t, "Directory", typeName)

		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), withDirField, map[string]any{
			"dir": "https://github.com/org/repo#main",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "nor a resolvable Directory address")
	})

	t.Run("host-reaching addresses are refused before resolution", func(t *testing.T) {
		// A bare path would go through Host.directory for the CLI; for a
		// model it is refused outright, naming the accepted forms, before
		// any Address lookup (this server has no Address.directory, so a
		// lookup would fail differently).
		withDirField := fieldByName(srv.Schema().Types["LiftTestRunner"], "withDir")
		require.NotNil(t, withDirField)
		for _, addr := range []string{"/etc", "file:///etc", "git@github.com:org/repo"} {
			_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), withDirField, map[string]any{
				"dir": addr,
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), fmt.Sprintf("%q is not a Directory ID or an accepted Directory address", addr))
			require.Contains(t, err.Error(), "https://, http:// or git:// git URL or a dag:// address")
			require.NotContains(t, err.Error(), "nor a resolvable")
		}
	})

	t.Run("blocklisted args do not lift", func(t *testing.T) {
		// Secret is addressable in the CLI, but blocklisted for tool args
		// (unliftableTypes): a plain string for a Secret arg surfaces the ID
		// decode error with NO address lookup attempted, so a model cannot
		// mint env:// secrets.
		withTokenField := fieldByName(srv.Schema().Types["LiftTestRunner"], "withToken")
		require.NotNil(t, withTokenField)
		_, ok := liftableObjectArg(withTokenField.Arguments.ForName("token"))
		require.False(t, ok)

		_, err := newMCP().buildObjectMethodSelector(ctx, srv, runner.ObjectType(), withTokenField, map[string]any{
			"token": "env://HOME",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), `arg "token": decode string`)
		require.NotContains(t, err.Error(), "address")
	})
}

// TestCheckLiftableAddress covers the forms a model may supply for a liftable
// arg: dag:// addresses and forms whose decoding stays off the calling
// client's host. Everything the Address decoders would read from the host —
// local paths, file:// URLs, ssh git URLs (the client's SSH agent), tcp://
// tunnels — is refused before decoding.
func TestCheckLiftableAddress(t *testing.T) {
	accepted := map[string][]string{
		"Container":     {"golang:1.26", "registry.example.com/org/img@sha256:abc", "dag://mod/ctr"},
		"Directory":     {"https://github.com/org/repo#main:docs", "git://example.com/repo", "http://example.com/repo.git", "dag://staff/members/workspace?member=chief"},
		"File":          {"https://github.com/org/repo#main:README.md", "dag+file://mod/readme"},
		"GitRef":        {"https://github.com/org/repo#main", "https://github.com/org/repo", "dag://staff/members/head?member=chief", "dag+git-ref://committer/saved/head?saved-workspace=abc"},
		"GitRepository": {"https://github.com/org/repo", "dag://mod/repo"},
		"Service":       {"dag://mod/server"},
	}
	for typeName, addrs := range accepted {
		for _, addr := range addrs {
			require.NoError(t, checkLiftableAddress(typeName, addr), "%s %q", typeName, addr)
		}
	}

	refused := map[string][]string{
		"Directory":     {".", "/etc", "./src", "~/secrets", "file:///etc", "file:.", "ssh://git@github.com/org/repo", "git@github.com:org/repo.git"},
		"File":          {"/etc/passwd", "README.md", "file:///etc/passwd", "git@github.com:org/repo#main:README.md"},
		"GitRef":        {".", "/home/me/repo#main", "file:///home/me/repo#main", "ssh://git@github.com/org/repo#main", "git@github.com:org/repo#main"},
		"GitRepository": {".", "../other", "file:///home/me/repo", "git@github.com:org/repo"},
		"Service":       {"tcp://localhost:8080", "udp://127.0.0.1:53", "localhost:8080"},
	}
	for typeName, addrs := range refused {
		for _, addr := range addrs {
			err := checkLiftableAddress(typeName, addr)
			require.Error(t, err, "%s %q", typeName, addr)
			require.Contains(t, err.Error(), "dag://", "the refusal names the accepted forms")
			require.Contains(t, err.Error(), "calling client's host")
		}
	}
}

// batchTestRunner is a receiver for deriving object tools from real dagql
// field specs, cache policy included.
type batchTestRunner struct{}

func (*batchTestRunner) Type() *ast.Type {
	return &ast.Type{NamedType: "BatchTestRunner", NonNull: true}
}

// TestObjectToolPurity covers which object tools CallBatch may run
// concurrently: those that neither change the agent's state nor are cached
// with policy Never.
func TestObjectToolPurity(t *testing.T) {
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*batchTestRunner]{Typed: &batchTestRunner{}}))
	srv.InstallObject(dagql.NewClass[*Changeset](srv))
	dagql.Fields[*batchTestRunner]{
		dagql.Func("read", func(context.Context, *batchTestRunner, struct{}) (dagql.String, error) {
			return "", nil
		}),
		// cacheImplicitInputs' encoding of @cache(policy: Never)...
		dagql.Func("deploy", func(context.Context, *batchTestRunner, struct{}) (dagql.String, error) {
			return "", nil
		}).WithInput(dagql.PerCallInput),
		// ...and of PerSession, which stays pure.
		dagql.Func("status", func(context.Context, *batchTestRunner, struct{}) (dagql.String, error) {
			return "", nil
		}).WithInput(dagql.PerSessionInput),
		// A core field's DoNotCache marks the same thing.
		dagql.Func("export", func(context.Context, *batchTestRunner, struct{}) (dagql.String, error) {
			return "", nil
		}).DoNotCache("writes to the host"),
		dagql.Func("edit", func(context.Context, *batchTestRunner, struct{}) (*Changeset, error) {
			return nil, nil
		}),
		dagql.Func("advance", func(context.Context, *batchTestRunner, struct{}) (*batchTestRunner, error) {
			return nil, nil
		}),
	}.Install(srv)
	objType, ok := srv.ObjectType("BatchTestRunner")
	require.True(t, ok)

	m := newMCP().WithLazyTools(nil, objType, srv.Schema(), nil)
	toolsets, err := m.boundToolsets(srv)
	require.NoError(t, err)
	require.Len(t, toolsets, 1)
	pure := map[string]bool{}
	for _, tool := range toolsets[0].tools {
		pure[tool.Name] = tool.ReadOnly
	}
	require.Equal(t, map[string]bool{
		"read":    true,
		"status":  true,
		"deploy":  false, // Never-cached: side effects or live reads
		"export":  false, // DoNotCache: the core spelling of the same
		"edit":    false, // returns a Changeset
		"advance": false, // rebinds the agent's state
	}, pure)

	// So a Never-cached call is a barrier between the pure calls around it.
	tools := toolsets[0].tools
	var calls []*LLMToolCall
	for i, name := range []string{"read", "status", "deploy", "read", "status"} {
		calls = append(calls, batchCall(t, i+1, name, nil))
	}
	steps := m.planBatch(tools, calls)
	require.Len(t, steps, 3)
	require.True(t, steps[0].pure)
	require.Equal(t, calls[0:2], steps[0].calls)
	require.False(t, steps[1].pure)
	require.Equal(t, calls[2:3], steps[1].calls)
	require.True(t, steps[2].pure)
	require.Equal(t, calls[3:5], steps[2].calls)
}
