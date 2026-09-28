package core

import (
	"context"
	"regexp"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// callInspectRecipe is the recipe the call inspection tests rebuild:
//
//	container.from(address: "alpine").withDirectory(path: "/src", directory: <git(url).tree>)
//
// The tree chain is reachable only as an ID argument, which is the shape
// the payload log channel exists for.
func callInspectRecipe(t *testing.T) *call.ID {
	t.Helper()
	dirType := &ast.Type{NamedType: "Directory", NonNull: true}
	ctrType := &ast.Type{NamedType: "Container", NonNull: true}
	tree := call.New().
		Append(&ast.Type{NamedType: "GitRepository", NonNull: true}, "git",
			call.WithArgs(call.NewArgument("url", call.NewLiteralString("https://example.com/repo"), false))).
		Append(dirType, "tree")
	return call.New().
		Append(ctrType, "container").
		Append(ctrType, "from",
			call.WithArgs(call.NewArgument("address", call.NewLiteralString("alpine"), false))).
		Append(ctrType, "withDirectory",
			call.WithArgs(
				call.NewArgument("path", call.NewLiteralString("/src"), false),
				call.NewArgument("directory", call.NewLiteralID(tree), false),
			))
}

// callInspectStore writes the recipe's frames into a store the way the
// engine delivers them: the root frame on its recording span (dag.digest +
// dag.call), every other frame as a call-payload log record -- minus the
// digests in withhold, which simulates frames that never reached the
// client. Returns the store, the root digest, and every frame by field.
func callInspectStore(t *testing.T, id *call.ID, withhold ...string) (*clientdb.DB, string, map[string]*callpbv1.Call) {
	t.Helper()
	ctx := context.Background()
	store, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "call-inspect-test")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	dag, err := id.ToProto()
	require.NoError(t, err)
	recipe := dag.GetRecipe()
	require.NotNil(t, recipe)
	byField := map[string]*callpbv1.Call{}
	for _, frame := range recipe.CallsByDigest {
		byField[frame.Field] = frame
	}

	held := map[string]bool{}
	for _, digest := range withhold {
		held[digest] = true
	}

	const traceID = "000102030405060708090a0b0c0d0e0f"
	stringAttr := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{
			Key:   key,
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}},
		}
	}
	root := recipe.CallsByDigest[recipe.RootDigest]
	encodedRoot, err := root.Encode()
	require.NoError(t, err)
	spans := []clientdb.Span{
		{TraceID: traceID, SpanID: "0000000000000001", Name: "root", Attributes: marshalSpanAttrs(t)},
		{TraceID: traceID, SpanID: "0000000000000002", ParentSpanID: validSpanID("0000000000000001"), Name: "Container.withDirectory",
			Attributes: marshalSpanAttrs(t,
				stringAttr(telemetry.DagDigestAttr, recipe.RootDigest),
				stringAttr(telemetry.DagCallAttr, encodedRoot))},
	}
	for i := range spans {
		spans[i].Links = []byte("[]")
		spans[i].Events = []byte("[]")
		spans[i].Resource = []byte("{}")
		spans[i].InstrumentationScope = []byte("{}")
	}
	_, err = store.AppendSpans(spans)
	require.NoError(t, err)

	var logs []clientdb.Log
	for digest, frame := range recipe.CallsByDigest {
		if digest == recipe.RootDigest || held[digest] {
			continue
		}
		payload, err := proto.Marshal(frame)
		require.NoError(t, err)
		body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: payload}})
		require.NoError(t, err)
		logs = append(logs, clientdb.Log{
			TraceID:    validSpanID(traceID),
			SpanID:     validSpanID("0000000000000002"),
			Body:       body,
			Attributes: marshalSpanAttrs(t, stringAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType)),
		})
	}
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)
	return store, recipe.RootDigest, byField
}

func TestInspectCallPreservesImplicitInputs(t *testing.T) {
	for _, transport := range []string{"span", "payload log"} {
		t.Run(transport, func(t *testing.T) {
			id := callInspectRecipe(t).With(call.WithImplicitInputs(
				call.NewArgument("cachePerClient", call.NewLiteralString("client-a"), false),
			))
			if transport == "payload log" {
				id = id.Append(&ast.Type{NamedType: "Container", NonNull: true}, "sync")
			}
			store, root, frames := callInspectStore(t, id)
			ctx := t.Context()
			want := frames["withDirectory"].ImplicitInputs
			require.Len(t, want, 1)

			// Prove both telemetry transports retain the input before ID extraction.
			db := dagui.NewDB()
			require.NoError(t, loadCallClosure(ctx, store, db, root))
			frame := db.Call(frames["withDirectory"].Digest)
			require.NotNil(t, frame)
			require.Len(t, frame.ImplicitInputs, 1)
			require.Equal(t, "cachePerClient", frame.ImplicitInputs[0].Name)
			require.Equal(t, "client-a", frame.ImplicitInputs[0].Value.GetString_())

			t.Run("recipe", func(t *testing.T) {
				loaded, err := loadRecipe(ctx, store, root)
				require.NoError(t, err)
				pb, err := loaded.id.ToProto()
				require.NoError(t, err)
				got := pb.GetRecipe().CallsByDigest[frames["withDirectory"].Digest]
				require.NotNil(t, got)
				require.Len(t, got.ImplicitInputs, len(want))
				require.True(t, proto.Equal(want[0], got.ImplicitInputs[0]))
			})
			t.Run("stats", func(t *testing.T) {
				got, err := inspectCallIn(ctx, store, root, callInspectOpts{View: callViewStats})
				require.NoError(t, err)
				t.Logf("stats:\n%s", got)
				require.Contains(t, got, "implicitInputs=1")
			})
			t.Run("find", func(t *testing.T) {
				got, err := inspectCallIn(ctx, store, root, callInspectOpts{
					View: callViewFind, Find: regexp.MustCompile(`withDirectory`),
				})
				require.NoError(t, err)
				t.Logf("find:\n%s", got)
				require.Contains(t, got, `implicit cachePerClient: "client-a"`)
			})
		})
	}
}

func TestInspectCallPreservesRecipeMetadata(t *testing.T) {
	implicit := call.New().Append(&ast.Type{NamedType: "Directory", NonNull: true}, "implicitDirectory")
	id := callInspectRecipe(t).With(
		call.WithImplicitInputs(call.NewArgument("scope", call.NewLiteralList(
			call.NewLiteralObject(call.NewArgument("directory", call.NewLiteralID(implicit), false)),
		), false)),
		call.WithEffectIDs([]string{"effect-a"}),
		call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("content-a"), Label: "content"}),
	)
	want, err := id.ToProto()
	require.NoError(t, err)
	store, root, _ := callInspectStore(t, id)

	t.Run("complete", func(t *testing.T) {
		loaded, err := loadRecipe(t.Context(), store, root)
		require.NoError(t, err)
		got, err := loaded.id.ToProto()
		require.NoError(t, err)
		require.True(t, proto.Equal(want, got), "all recipe frames and metadata must round-trip unchanged")
		stats, err := inspectCallIn(t.Context(), store, root, callInspectOpts{View: callViewStats})
		require.NoError(t, err)
		require.Contains(t, stats, "distinct calls: 6")
		require.Contains(t, stats, "extraDigests=1 effectIds=1 implicitInputs=1")
		found, err := inspectCallIn(t.Context(), store, root, callInspectOpts{
			View: callViewFind, Find: regexp.MustCompile(`implicitDirectory`),
		})
		require.NoError(t, err)
		require.Contains(t, found, "digest:     "+implicit.Digest().String())
	})
	t.Run("missing implicit frame", func(t *testing.T) {
		store, root, _ := callInspectStore(t, id, implicit.Digest().String())
		_, err := loadRecipe(t.Context(), store, root)
		require.ErrorContains(t, err, implicit.Digest().String()+" never reached this client")
		require.ErrorContains(t, err, `referenced as implicit input "scope"`)
	})
}

func TestInspectCallIn(t *testing.T) {
	ctx := context.Background()
	id := callInspectRecipe(t)
	store, root, frames := callInspectStore(t, id)

	// chain: the receiver spine in the TUI's own vocabulary.
	got, err := inspectCallIn(ctx, store, root, callInspectOpts{View: callViewChain})
	require.NoError(t, err)
	require.Contains(t, got, "container")
	require.Contains(t, got, `from(address: "alpine")`)
	require.Contains(t, got, "withDirectory")

	// tree: the ID argument's chain expanded inline, so the frames that
	// only ever rode the log channel are there.
	got, err = inspectCallIn(ctx, store, root, callInspectOpts{View: callViewTree})
	require.NoError(t, err)
	require.Contains(t, got, "== spine of Container.withDirectory (3 selectors) ==")
	require.Contains(t, got, "arg:directory = ID (2 selectors)")
	require.Contains(t, got, `git(url: "https://example.com/repo")`)

	got, err = inspectCallIn(ctx, store, root, callInspectOpts{View: callViewStats})
	require.NoError(t, err)
	require.Contains(t, got, "== "+root+" ==")
	require.Contains(t, got, "distinct calls: 5")
	require.Contains(t, got, "root:          Container.withDirectory Container!")

	got, err = inspectCallIn(ctx, store, root, callInspectOpts{View: callViewFind, Find: regexp.MustCompile(`GitRepository\.tree`)})
	require.NoError(t, err)
	require.Contains(t, got, "== 1 call(s) matching")
	require.Contains(t, got, "digest:     "+frames["tree"].Digest)

	_, err = inspectCallIn(ctx, store, root, callInspectOpts{View: callViewFind})
	require.ErrorContains(t, err, "needs a `find` pattern")

	// diff: any frame's recipe is inspectable, not just the spanned root,
	// and the two are compared structurally.
	got, err = inspectCallIn(ctx, store, root, callInspectOpts{Diff: frames["from"].Digest})
	require.NoError(t, err)
	require.Contains(t, got, "calls:    5 -> 2 (-3)")
	require.Contains(t, got, "root spine: 3 -> 2 selectors, identical prefix of 2")

	_, err = inspectCallIn(ctx, store, "xxh3:nope", callInspectOpts{})
	require.ErrorContains(t, err, "call xxh3:nope never reached this client")
}

// A gap in the closure is reported with the frame that referenced it, in
// full -- the only description of a frame the client never got.
func TestInspectCallReportsMissingFrame(t *testing.T) {
	ctx := context.Background()
	id := callInspectRecipe(t)
	probe, _, frames := callInspectStore(t, id)
	probe.Close()
	store, root, _ := callInspectStore(t, id, frames["tree"].Digest)

	_, err := inspectCallIn(ctx, store, root, callInspectOpts{})
	require.ErrorContains(t, err, "call "+frames["tree"].Digest+" never reached this client")
	require.ErrorContains(t, err, `referenced as argument "directory"`)
	require.ErrorContains(t, err, "withDirectory")
}

func TestSpanCallDigest(t *testing.T) {
	ctx := context.Background()
	store, root, _ := callInspectStore(t, callInspectRecipe(t))

	got, err := spanCallDigest(ctx, store, "0000000000000002")
	require.NoError(t, err)
	require.Equal(t, root, got)

	_, err = spanCallDigest(ctx, store, "0000000000000001")
	require.ErrorContains(t, err, "not a dagql call")

	_, err = spanCallDigest(ctx, store, "00000000000000ff")
	require.ErrorContains(t, err, "no span")
}

func TestFindCallsIn(t *testing.T) {
	ctx := context.Background()
	store, root, frames := callInspectStore(t, callInspectRecipe(t))

	// A literal deep in an argument, on a frame that only rode the log
	// channel; the line names the digest to inspect next.
	got, err := findCallsIn(ctx, store, regexp.MustCompile(`example\.com`), 0)
	require.NoError(t, err)
	require.Equal(t, frames["git"].Digest+`  git(url: "https://example.com/repo") -> GitRepository`+"\n", got)

	// The spanned root is searchable too, and names its receiver so the
	// chain can be walked.
	got, err = findCallsIn(ctx, store, regexp.MustCompile(`withDirectory`), 0)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, root+"  withDirectory("), got)
	require.Contains(t, got, "recv="+frames["from"].Digest)

	got, err = findCallsIn(ctx, store, regexp.MustCompile(`.`), 2)
	require.NoError(t, err)
	require.Len(t, strings.Split(strings.TrimSpace(got), "\n"), 3)
	require.Contains(t, got, "… 3 more")

	got, err = findCallsIn(ctx, store, regexp.MustCompile(`nothing`), 0)
	require.NoError(t, err)
	require.Equal(t, "(no calls matching /nothing/ among the 5 in this session)", got)
}
