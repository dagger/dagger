package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// A workspace made from another keeps the content identity of what it holds,
// and its result carries that identity as its content digest, so a session
// that makes the same workspace another way meets an earlier session's reads
// of it. Module loading reads a module's files through a workspace made by
// withWorkdir from the session's own, whose recipe is new in every session:
// without the digest, those reads met nothing made in another session, were
// made again in every one, and the Cloud kept a call each time.
func TestWorkspaceMadeAnotherWayMeetsEarlierReads(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	file := func(t *testing.T, ctx context.Context) dagql.ObjectResult[*core.Directory] {
		t.Helper()
		var dir dagql.ObjectResult[*core.Directory]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &dir,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("sub/notes.txt")},
				{Name: "contents", Value: dagql.NewString("notes")},
			}}))
		return dir
	}
	read := func(t *testing.T, ctx context.Context, ws dagql.ObjectResult[*core.Workspace]) dagql.ObjectResult[*core.Directory] {
		t.Helper()
		var dir dagql.ObjectResult[*core.Directory]
		require.NoError(t, e.dag.Select(ctx, ws, &dir, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(".")},
			{Name: "include", Value: dagql.ArrayInput[dagql.String]{"notes.txt"}},
		}}))
		return dir
	}
	identity := func(t *testing.T, ctx context.Context, ws dagql.ObjectResult[*core.Workspace]) string {
		t.Helper()
		id, ok, err := ws.Self().ContentIdentity(ctx)
		require.NoError(t, err)
		require.True(t, ok)
		return id.String()
	}

	// The first session reads through a directory's workspace, its working
	// directory moved by withWorkdir.
	first := e.session(t, "first")
	var moved dagql.ObjectResult[*core.Workspace]
	require.NoError(t, e.dag.Select(first, file(t, first), &moved,
		dagql.Selector{Field: "asWorkspace"},
		dagql.Selector{Field: "withWorkdir", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("sub")}}}))
	require.NoError(t, e.cache.Evaluate(first, read(t, first, moved)))

	// A later session makes the same workspace another way, its working
	// directory given at once, and makes the same read: it is the first
	// session's, made nothing again.
	later := e.session(t, "later")
	var given dagql.ObjectResult[*core.Workspace]
	require.NoError(t, e.dag.Select(later, file(t, later), &given,
		dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString("sub")}}}))
	require.Equal(t, identity(t, first, moved), identity(t, later, given), "the two workspaces hold the same")
	require.True(t, read(t, later, given).HitCache(), "the later session's read is the first's, not made again")
}

// An engine served another's read as the Cloud serves it, its entry with the
// call that returned it and that call's receiver, meets it through the same
// workspace made another way: the receiver's record carries the workspace's
// identity, which the engine's own workspace has too. A fresh engine on a
// whole pull request meets the other engines' module loads so; without the
// identity on a workspace made by withWorkdir, it made every read again.
func TestWorkspaceMadeAnotherWayMeetsAnotherEnginesKeptRead(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	file := func(t *testing.T, e *transferTestEngine, ctx context.Context) dagql.ObjectResult[*core.Directory] {
		t.Helper()
		var dir dagql.ObjectResult[*core.Directory]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &dir,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("sub/notes.txt")},
				{Name: "contents", Value: dagql.NewString("notes")},
			}}))
		return dir
	}
	read := func(t *testing.T, e *transferTestEngine, ctx context.Context, ws dagql.ObjectResult[*core.Workspace]) dagql.ObjectResult[*core.Directory] {
		t.Helper()
		var dir dagql.ObjectResult[*core.Directory]
		require.NoError(t, e.dag.Select(ctx, ws, &dir, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(".")},
			{Name: "include", Value: dagql.ArrayInput[dagql.String]{"notes.txt"}},
		}}))
		return dir
	}

	// Engine A reads through a directory's workspace moved by withWorkdir,
	// its call reported on its span as the Cloud reads it.
	a := newTransferTestEngine(t, salt)
	a1 := a.session(t, "a1")
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.WithoutCancel(a1))
	traced, span := provider.Tracer("engine-a").Start(a1, "reads")
	var moved dagql.ObjectResult[*core.Workspace]
	require.NoError(t, a.dag.Select(traced, file(t, a, traced), &moved,
		dagql.Selector{Field: "asWorkspace"},
		dagql.Selector{Field: "withWorkdir", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("sub")}}}))
	readA := read(t, a, traced, moved)
	require.NoError(t, a.cache.Evaluate(traced, readA))
	span.End()
	var call string
	for _, ended := range recorder.Ended() {
		if ended.Name() != "Workspace.directory" {
			continue
		}
		for _, kv := range ended.Attributes() {
			if string(kv.Key) == "dagger.io/dag.call" && kv.Value.AsString() != "" {
				call = kv.Value.AsString()
			}
		}
	}
	require.NotEmpty(t, call, "A's read span carries its call")
	readNumber, ok := dagql.CacheResultNumber(readA)
	require.True(t, ok)

	// Engine B is sent what the Cloud sends for it: the read's entry and its
	// content, the call that returned it, and that call's receiver.
	b := newTransferTestEngine(t, salt)
	selection := dagql.ValueSelection{
		Roots:   []dagql.AnyResult{readA, moved},
		Outputs: []dagql.SelectedValueOutput{{Result: readA, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
	}
	require.NoError(t, a.cache.WithExportedValues(a1, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		bundle := exported.Bundle
		found := false
		for _, v := range bundle.Values {
			if v.SenderNumber == readNumber {
				bundle.Calls = append(bundle.Calls, dagql.TransferredCall{Call: call, Value: v.Ordinal})
				found = true
			}
		}
		require.True(t, found, "the read's entry is in the bundle")
		require.Len(t, exported.Chains.Entries, 1, "the read's content is offered")
		b.cache.SetPartContentSource(fileContentsTestSource{&testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}})
		b1 := b.session(t, "b1")
		_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, bundle)
		require.NoError(t, err)

		// B makes the same workspace another way, its working directory
		// given at once, and makes the same read: it is A's.
		var given dagql.ObjectResult[*core.Workspace]
		require.NoError(t, b.dag.Select(b1, file(t, b, b1), &given,
			dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString("sub")}}}))
		require.True(t, read(t, b, b1, given).HitCache(), "B's read is A's, not made again")
		return nil
	}))
}

// A workspace without a content identity, read from a client's host, keeps
// its result as it is: its files can change between calls.
func TestHostWorkspaceResultCarriesNoIdentity(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	ctx := e.session(t, "host")
	host := &core.Workspace{ClientID: "host-client"}
	host.SetHostPath("/src")
	host.SetSource(core.NewWorkspaceSourceClientLocal("/src"))
	require.False(t, host.HasContentIdentity())
	res, err := dagql.NewObjectResultForCall(host, e.dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "host-workspace",
		Type:        dagql.NewResultCallType(host.Type()),
	})
	require.NoError(t, err)
	got := identifiedWorkspaceResult(ctx, res)
	frame, err := got.ResultCall()
	require.NoError(t, err)
	require.Empty(t, frame.ContentDigest(), "a host workspace is not identified by its content")
}
