package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type CallPayloadClosureSuite struct{}

func TestCallPayloadClosure(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CallPayloadClosureSuite{})
}

// unrebuildableCallSpans lists every call span whose call cannot be turned
// back into an ID from db: some frame of its closure never reached it, as a
// span or a payload log.
func unrebuildableCallSpans(db *dagui.DB) []string {
	var problems []string
	seen := map[string]bool{}
	for span := range db.AllSpans() {
		// Exec spans name an exec identity, not a dagql call.
		if !strings.HasPrefix(span.CallDigest, "xxh3:") || seen[span.CallDigest] {
			continue
		}
		seen[span.CallDigest] = true
		if missing := missingFrames(db, span.CallDigest); len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("%s (%s): frames never received: %v", span.Name, span.CallDigest, missing))
		}
	}
	sort.Strings(problems)
	return problems
}

// missingFrames lists the frames of a call's closure that db holds no exact
// payload for. Unlike DB.CallIDForDigest it does not fall back to a creator
// span (one whose output is the digest): that answers with a different
// recipe, so it cannot stand in for the frame a span names.
func missingFrames(db *dagui.DB, dig string) []string {
	var missing []string
	seen := map[string]bool{}
	var visitCall func(string)
	var visitLit func(*callpbv1.Literal)
	visitLit = func(lit *callpbv1.Literal) {
		switch v := lit.GetValue().(type) {
		case *callpbv1.Literal_CallDigest:
			visitCall(v.CallDigest)
		case *callpbv1.Literal_List:
			for _, val := range v.List.GetValues() {
				visitLit(val)
			}
		case *callpbv1.Literal_Object:
			for _, field := range v.Object.GetValues() {
				visitLit(field.GetValue())
			}
		}
	}
	visitCall = func(dig string) {
		if dig == "" || seen[dig] {
			return
		}
		seen[dig] = true
		call, ok := db.Calls[dig]
		if !ok {
			missing = append(missing, dig)
			return
		}
		visitCall(call.GetReceiverDigest())
		visitCall(call.GetModule().GetCallDigest())
		for _, arg := range call.GetArgs() {
			visitLit(arg.GetValue())
		}
		for _, input := range call.GetImplicitInputs() {
			visitLit(input.GetValue())
		}
	}
	visitCall(dig)
	return missing
}

// traceDB folds what the sink received for one trace, and nothing else, into
// a fresh DB: what a reader of that trace alone (Cloud, an archive, a
// resumed session) gets to rebuild calls from.
func (sink *agentTraceSink) traceDB(ctx context.Context, t testing.TB, traceID []byte) *dagui.DB {
	t.Helper()
	traces, logs := sink.capture()
	db := dagui.NewDB()
	for _, req := range traces {
		req = proto.Clone(req).(*coltracepb.ExportTraceServiceRequest)
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				kept := ss.Spans[:0]
				for _, span := range ss.Spans {
					if bytes.Equal(span.GetTraceId(), traceID) {
						kept = append(kept, span)
					}
				}
				ss.Spans = kept
			}
		}
		require.NoError(t, db.ExportSpans(ctx, telemetry.SpansFromPB(req.ResourceSpans)))
	}
	logExp := db.LogExporter()
	for _, req := range logs {
		req = proto.Clone(req).(*collogspb.ExportLogsServiceRequest)
		for _, rl := range req.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				kept := sl.LogRecords[:0]
				for _, rec := range sl.LogRecords {
					if bytes.Equal(rec.GetTraceId(), traceID) {
						kept = append(kept, rec)
					}
				}
				sl.LogRecords = kept
			}
		}
		require.NoError(t, telemetry.ReexportLogsFromPB(ctx, logExp, req.GetResourceLogs()))
	}
	return db
}

// requireTraceRebuildable waits for a call span named wantSpan, then requires
// every call span of its trace to rebuild from that trace's telemetry alone.
func (sink *agentTraceSink) requireTraceRebuildable(ctx context.Context, t *testctx.T, wantSpan string) {
	t.Helper()
	var traceID []byte
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		sink.read(func(db *dagui.DB) {
			for span := range db.AllSpans() {
				if span.Name == wantSpan && span.CallDigest != "" {
					id := span.TraceID.TraceID
					traceID = id[:]
				}
			}
			assert.NotNil(ct, traceID, "no %s call span yet", wantSpan)
		})
	}, 30*time.Second, 200*time.Millisecond)
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		problems := unrebuildableCallSpans(sink.traceDB(ctx, t, traceID))
		assert.Empty(ct, problems, "call spans that do not rebuild from their trace:\n%s", strings.Join(problems, "\n"))
	}, 30*time.Second, 500*time.Millisecond)
}

func runQuery(ctx context.Context, t *testctx.T, c *dagger.Client, query string) {
	t.Helper()
	res := map[string]any{}
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: query}, &dagger.Response{Data: &res}))
}

// untracedNestedQuery posts $QUERY to the exec's nested session without a
// traceparent, as a client calling with no span in its context does.
const untracedNestedQuery = `set -e
auth=$(printf '%s:' "$DAGGER_SESSION_TOKEN" | base64 | tr -d '\n')
wget -q -O- --header "Authorization: Basic $auth" --header "Content-Type: application/json" \
  --post-data "$QUERY" "http://127.0.0.1:$DAGGER_SESSION_PORT/query"
`

// runUntracedNested runs query as a nested client of c's session, whose
// delivery route includes c's client, with no traceparent on its request.
// files are written to the exec's working directory, /work.
func runUntracedNested(ctx context.Context, t *testctx.T, c *dagger.Client, query string, files map[string]string) {
	t.Helper()
	body := fmt.Sprintf(`{"query":%q}`, query)
	ctr := core.NewQuery(c).Container().From(alpineImage).WithWorkdir("/work")
	for path, contents := range files {
		ctr = ctr.WithNewFile(path, contents)
	}
	out, err := ctr.
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithEnvVariable("QUERY", body).
		WithExec([]string{"sh", "-c", untracedNestedQuery}, core.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `"data"`)
	require.NotContains(t, out, `"errors"`)
}

// TestUntracedNestedMintsChain: a nested client's request without a
// traceparent evaluates a chain first; then the parent client, traced, builds
// on it. The parent's trace must carry every frame its call spans name, even
// though the untraced request reached those calls first.
func (CallPayloadClosureSuite) TestUntracedNestedMintsChain(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	contents := rand.Text()
	runUntracedNested(ctx, t, c, fmt.Sprintf(`{ directory { withNewFile(path: "a", contents: %q) { withNewFile(path: "b", contents: "b") { entries } } } }`, contents), nil)
	runQuery(ctx, t, c, fmt.Sprintf(`{ directory { withNewFile(path: "a", contents: %q) { withNewFile(path: "b", contents: "b") { withNewFile(path: "c", contents: "c") { entries } } } } }`, contents))
	require.NoError(t, c.Close())
	sink.requireTraceRebuildable(ctx, t, "Directory.withNewFile")
}

// goModuleWorkspaceFiles is a workspace that pins github.com/dagger/go@v0.5.0
// in its lock, as a checkout of dagger/dagger does. .git marks its root.
var goModuleWorkspaceFiles = map[string]string{
	".git/HEAD": "ref: refs/heads/main\n",
	"dagger.toml": `[modules.golang]
source = "github.com/dagger/go@v0.5.0"
`,
	"dagger.lock": `[["version","2"]]
["","git-latest",["github.com/dagger/go",["version","v0.5.0"]],"refs/tags/v0.5.0"]
["","git-sha",["github.com/dagger/go","refs/tags/v0.5.0"],"262b66dc9683ed6c2bcd55a8f5b85eff62b1c152"]
`,
}

// TestUntracedLockRefreshThenModuleSource is the live failure end to end: a
// tool's exec refreshes a workspace lock over its nested session with a bare
// curl, no traceparent. The refresh resolves the git ref under a fresh cache
// scope, and the parent's own traced moduleSource of the same ref then gets
// the refresh's result back, its frame naming the scoped repository.
func (CallPayloadClosureSuite) TestUntracedLockRefreshThenModuleSource(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	runUntracedNested(ctx, t, c,
		`{ currentWorkspace { __withUpdatedLock(selectors: ["github.com/dagger/go"], noGenerate: true) { file(path: "dagger.lock") { contents } } } }`,
		goModuleWorkspaceFiles)
	runQuery(ctx, t, c, `{ moduleSource(refString: "github.com/dagger/go@v0.5.0") { moduleName } }`)
	require.NoError(t, c.Close())
	sink.requireTraceRebuildable(ctx, t, "GitRef.tree")
}

// TestUntracedNestedMintsGitChain is the shape of the live failure: an
// untraced nested request resolves a git ref first, and the parent's traced
// GitRef.tree names that ref as its receiver.
func (CallPayloadClosureSuite) TestUntracedNestedMintsGitChain(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	runUntracedNested(ctx, t, c, `{ git(url: "https://github.com/dagger/go") { latest(version: "v0.5.0") { commit } } }`, nil)
	runQuery(ctx, t, c, `{ git(url: "https://github.com/dagger/go") { latest(version: "v0.5.0") { tree { entries } } } }`)
	require.NoError(t, c.Close())
	sink.requireTraceRebuildable(ctx, t, "GitRef.tree")
}
