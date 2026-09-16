package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type lazyOperationFixture struct {
	env     *persistedFamiliesTestEnv
	ctx     context.Context
	cache   *dagql.Cache
	srv     *dagql.Server
	recipes []Lazy[*Directory]
	inputs  []dagql.AnyResult
}

func newLazyOperationFixture(t *testing.T) *lazyOperationFixture {
	t.Helper()
	env := newPersistedFamiliesTestEnv(t, "eager-operations")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitCommit]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitBundle]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*HTTPState]{}))
	dir := env.directory(t, ctx, cache, srv, "source", "source-snapshot")
	repo := env.attach(t, ctx, cache, srv, "repo", &GitRepository{Backend: &LocalGitRepository{Directory: dir}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	file := storedSnapshotTestValue("File", "bundle-snapshot", "/repo.bundle", false).(*File)
	fileRes := env.attach(t, ctx, cache, srv, "bundle-file", file).(dagql.ObjectResult[*File])
	bundle := env.attach(t, ctx, cache, srv, "bundle", &GitBundle{File: fileRes, Version: 2, ObjectFormat: "sha1", PrerequisiteSHAs: []string{"first", "second"}}).(dagql.ObjectResult[*GitBundle])
	refValue := &gitutil.Ref{Name: "refs/heads/main", SHA: strings.Repeat("a", 40)}
	backend, err := repo.Self().Backend.Get(ctx, refValue)
	require.NoError(t, err)
	ref := env.attach(t, ctx, cache, srv, "ref", &GitRef{Repo: repo, Ref: refValue, Backend: backend}).(dagql.ObjectResult[*GitRef])
	commit := env.attach(t, ctx, cache, srv, "commit", &GitCommit{Repo: repo, Ref: &gitutil.Ref{SHA: refValue.SHA}, FetchRef: refValue, Backend: backend}).(dagql.ObjectResult[*GitCommit])
	return &lazyOperationFixture{env: env, ctx: ctx, cache: cache, srv: srv, inputs: []dagql.AnyResult{repo, bundle, ref, commit}, recipes: []Lazy[*Directory]{
		&DirectoryGitCleanedLazy{LazyState: NewLazyState(), Repo: repo},
		&DirectoryGitBundleImportLazy{LazyState: NewLazyState(), Repo: repo, Bundle: bundle, PrerequisiteRef: "refs/heads/main"},
		&DirectoryGitTreeLazy{LazyState: NewLazyState(), Ref: ref, DiscardGitDir: true, Depth: 0, IncludeTags: false},
		&DirectoryGitCommitTreeLazy{LazyState: NewLazyState(), Commit: commit, DiscardGitDir: false, Depth: 7, IncludeTags: true},
		&DirectoryScratchLazy{LazyState: NewLazyState()},
	}}
}

func attachLazyOperationDirectory(t *testing.T, f *lazyOperationFixture, dir *Directory, recipe Lazy[*Directory]) dagql.AnyResult {
	t.Helper()
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Type: dagql.NewResultCallType(dir.Type())}
	var receiver dagql.AnyResult
	switch recipe := recipe.(type) {
	case *DirectoryScratchLazy:
		frame.Field = "directory"
	case *DirectoryGitCleanedLazy:
		frame.Field = "__cleaned"
		receiver = recipe.Repo
	case *DirectoryGitBundleImportLazy:
		frame.Field = "__withBundleDirectory"
		receiver = recipe.Repo
		frame.Args = []*dagql.ResultCallArg{{Name: "bundle", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindResultRef, ResultRef: &dagql.ResultCallRef{ResultID: persistedRowID(t, f.cache, recipe.Bundle)}}}}
	case *DirectoryGitTreeLazy:
		frame.Field = "tree"
		receiver = recipe.Ref
	case *DirectoryGitCommitTreeLazy:
		frame.Field = "tree"
		receiver = recipe.Commit
	default:
		t.Fatalf("unexpected operation %T", recipe)
	}
	if receiver != nil {
		frame.Receiver = &dagql.ResultCallRef{ResultID: persistedRowID(t, f.cache, receiver)}
	}
	result, err := f.cache.GetOrInitCall(f.ctx, f.env.session, f.srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewObjectResultForCall(dir, f.srv, frame) })
	require.NoError(t, err)
	return result
}

func TestLazyOperationCodecs(t *testing.T) {
	f := newLazyOperationFixture(t)
	enc := dagql.NewPersistEncodeContext(f.cache, 0, nil)
	dec := dagql.NewPersistDecodeContext(f.srv, 0, nil)
	for _, recipe := range f.recipes {
		t.Run(fmt.Sprintf("%T", recipe), func(t *testing.T) {
			kind, raw, err := encodePersistedDirectoryLazy(f.ctx, enc, recipe)
			require.NoError(t, err)
			decoded, err := decodePersistedDirectoryLazy(f.ctx, dec, kind, raw)
			require.NoError(t, err)
			require.NotSame(t, recipe, decoded)
			if scratch, ok := decoded.(*DirectoryScratchLazy); ok {
				require.Equal(t, "{}", string(raw))
				require.False(t, scratch.lazyInitComplete.Load())
				require.NotSame(t, recipe.(*DirectoryScratchLazy).LazyMu, scratch.LazyMu)
				deps, err := scratch.AttachDependencies(f.ctx, func(dagql.AnyResult) (dagql.AnyResult, error) { t.Fatal("scratch attached an input"); return nil, nil })
				require.NoError(t, err)
				require.Empty(t, deps)
				for _, savedKind := range []string{"scratch", ""} {
					payload, err := json.Marshal(persistedDirectoryPayload{Platform: Platform{OS: "linux", Architecture: "arm64"}, LazyKind: savedKind, LazyJSON: raw})
					require.NoError(t, err)
					route, err := foreignFamilyCodec("Directory").RouteParts(dagql.PersistedPayloadVisit{Payload: payload, Path: dagql.PersistedRefPath{}.Field("items").Index(2)}, "snapshot")
					require.NoError(t, err)
					require.Equal(t, savedKind != "", route.HasLazyOperation)
					if savedKind != "" {
						require.Equal(t, dagql.LazyGroupWhole, route.Group.Group)
						require.Equal(t, []dagql.PersistedPartAddress{{OutputPath: route.Group.OutputPath, Part: "snapshot"}}, route.WriteSet)
					}
				}
				for _, payload := range []string{"", "null", "[]", "0", `"{}"`, `{"platform":"linux/amd64"}`, `{"ignored":null}`, `{} {}`, `{`} {
					_, err := decodePersistedDirectoryLazy(f.ctx, dec, kind, json.RawMessage(payload))
					require.Error(t, err, payload)
					_, err = persistedDirectoryLazyVisitors[kind](json.RawMessage(payload), newPersistedRefWalker(func(*dagql.PersistedRef) error { t.Fatal("scratch visited a reference"); return nil }, nil))
					require.Error(t, err, payload)
				}
				for _, payload := range []string{`{}`, " \n{ \t }\n"} {
					_, err := decodePersistedDirectoryLazy(f.ctx, dec, kind, json.RawMessage(payload))
					require.NoError(t, err)
					out, err := persistedDirectoryLazyVisitors[kind](json.RawMessage(payload), newPersistedRefWalker(func(*dagql.PersistedRef) error { t.Fatal("scratch visited a reference"); return nil }, nil))
					require.NoError(t, err)
					require.Equal(t, payload, string(out))
				}
			}
			_, again, err := encodePersistedDirectoryLazy(f.ctx, enc, decoded)
			require.NoError(t, err)
			require.JSONEq(t, string(raw), string(again))
			for _, ready := range []bool{false, true} {
				formRecipe, err := decodePersistedDirectoryLazy(f.ctx, dec, kind, raw)
				require.NoError(t, err)
				dir := &Directory{Platform: Platform{OS: "linux", Architecture: "amd64"}, Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
				if ready {
					dir.Dir.setValue("/saved")
					dir.Snapshot.setValue(&cacheVolumeTestImmutableRef{id: "saved", snapshotID: "saved"})
					require.NoError(t, evaluatedLazyFixture(dir, formRecipe))
				} else {
					dir.Lazy = formRecipe
				}
				res := f.env.attach(t, f.ctx, f.cache, f.srv, fmt.Sprintf("%s-%v", kind, ready), dir)
				rowID := persistedRowID(t, f.cache, res)
				call, err := res.ResultCall()
				require.NoError(t, err)
				encoded, err := dir.EncodePersistedObject(f.ctx, dagql.NewPersistEncodeContext(f.cache, rowID, call))
				require.NoError(t, err)
				value, err := dir.DecodePersistedObject(f.ctx, dagql.NewPersistDecodeContext(f.srv, rowID, call), encoded.JSON)
				require.NoError(t, err)
				output := value.(*Directory)
				if ready {
					require.Equal(t, kind, output.lazyKind)
					require.JSONEq(t, string(raw), string(output.lazyJSON))
					require.IsType(t, &DirectoryRestoreLazy{}, output.Lazy)
				} else {
					require.IsType(t, recipe, output.Lazy)
				}
			}
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &fields))
			for key := range fields {
				if strings.HasSuffix(key, "ResultID") {
					bad := map[string]json.RawMessage{}
					for k, v := range fields {
						bad[k] = v
					}
					bad[key] = json.RawMessage(`0`)
					payload, err := json.Marshal(bad)
					require.NoError(t, err)
					_, err = decodePersistedDirectoryLazy(f.ctx, dec, kind, payload)
					require.ErrorContains(t, err, key)
					_, err = persistedDirectoryLazyVisitors[kind](payload, newPersistedRefWalker(func(*dagql.PersistedRef) error { t.Fatal("invalid ID reached visitor"); return nil }, dagql.PersistedRefPath{}))
					require.ErrorContains(t, err, key)
					bad[key] = json.RawMessage(fmt.Sprint(persistedRowID(t, f.cache, f.recipes[1].(*DirectoryGitBundleImportLazy).Bundle.Self().File)))
					payload, err = json.Marshal(bad)
					require.NoError(t, err)
					_, err = decodePersistedDirectoryLazy(f.ctx, dec, kind, payload)
					require.Error(t, err)
				}
			}
		})
	}
	t.Run("HTTP scalars", func(t *testing.T) {
		for i, checksum := range []dagql.Optional[dagql.String]{{}, {Valid: true}, {Valid: true, Value: dagql.String("sha256:" + strings.Repeat("b", 64))}} {
			original := &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: "https://origin/data", Filename: "a/../data", Permissions: 0, Checksum: checksum, BodyDigest: digest.FromString("body")}
			kind, raw, err := encodePersistedFileLazy(f.ctx, enc, original)
			require.NoError(t, err)
			require.Equal(t, "httpResolve", kind)
			decoded, err := decodePersistedFileLazy(f.ctx, dec, kind, raw)
			require.NoError(t, err)
			got := decoded.(*FileHTTPResolveLazy)
			require.Equal(t, original.Checksum, got.Checksum)
			require.Equal(t, original.BodyDigest, got.BodyDigest)
			require.Equal(t, original.Filename, got.Filename)
			require.Zero(t, got.Permissions)
			for _, ready := range []bool{false, true} {
				formRecipe, err := decodePersistedFileLazy(f.ctx, dec, kind, raw)
				require.NoError(t, err)
				file := freshLazyOperationFile()
				if ready {
					file.File.setValue(original.Filename)
					file.Snapshot.setValue(&cacheVolumeTestImmutableRef{id: "http-saved", snapshotID: "http-saved"})
					require.NoError(t, evaluatedLazyFixture(file, formRecipe))
				} else {
					file.Lazy = formRecipe
				}
				res := f.env.attach(t, f.ctx, f.cache, f.srv, fmt.Sprintf("http-%d-%v", i, ready), file)
				rowID := persistedRowID(t, f.cache, res)
				call, err := res.ResultCall()
				require.NoError(t, err)
				encoded, err := file.EncodePersistedObject(f.ctx, dagql.NewPersistEncodeContext(f.cache, rowID, call))
				require.NoError(t, err)
				restored, err := file.DecodePersistedObject(f.ctx, dagql.NewPersistDecodeContext(f.srv, rowID, call), encoded.JSON)
				require.NoError(t, err)
				if ready {
					require.Equal(t, kind, restored.(*File).lazyKind)
					require.JSONEq(t, string(raw), string(restored.(*File).lazyJSON))
				} else {
					require.IsType(t, original, restored.(*File).Lazy)
				}
			}

			_, err = persistedFileLazyVisitors[kind](raw, newPersistedRefWalker(func(*dagql.PersistedRef) error { t.Fatal("HTTP recipe declared a child"); return nil }, dagql.PersistedRefPath{}))
			require.NoError(t, err)
		}
		for _, invalid := range []string{"", "sha256:no", "sha256:" + strings.Repeat("A", 64), "sha512:" + strings.Repeat("a", 128)} {
			operation := &FileHTTPResolveLazy{BodyDigest: digest.Digest(invalid)}
			_, err := operation.EncodePersisted(f.ctx, enc)
			require.Error(t, err)
			raw, err := json.Marshal(persistedFileHTTPResolveLazy{BodyDigest: invalid})
			require.NoError(t, err)
			_, err = decodeFileHTTPResolveLazy(raw)
			require.Error(t, err)
			_, err = persistedFileLazyVisitors[persistedFileLazyKindHTTPResolve](raw, newPersistedRefWalker(nil, dagql.PersistedRefPath{}))
			require.Error(t, err)
		}
	})
	t.Run("builtin field pairing", func(t *testing.T) {
		operation := &ContainerBuiltinLazy{LazyState: NewLazyState(), Platform: Platform{OS: "linux", Architecture: "arm64"}, ManifestDigest: digest.FromString("manifest")}
		for _, call := range []*dagql.ResultCall{nil, {Field: "from"}} {
			_, err := operation.EncodePersisted(f.ctx, dagql.NewPersistEncodeContext(f.cache, 0, call))
			require.ErrorContains(t, err, "_builtinContainer")
		}
		call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "_builtinContainer", Type: dagql.NewResultCallType((&Container{}).Type())}
		raw, err := operation.EncodePersisted(f.ctx, dagql.NewPersistEncodeContext(f.cache, 0, call))
		require.NoError(t, err)
		decoded, err := decodePersistedContainerRecipe(f.ctx, dagql.NewPersistDecodeContext(f.srv, 0, call), call, raw)
		require.NoError(t, err)
		got := decoded.(*ContainerBuiltinLazy)
		require.Equal(t, operation.Platform, got.Platform)
		require.Equal(t, operation.ManifestDigest, got.ManifestDigest)
		require.NotSame(t, operation.LazyMu, got.LazyMu)
		for _, ready := range []bool{false, true} {
			fixture := newLazyOperationFixture(t)
			container := NewContainer(operation.Platform)
			if ready {
				container.FS.setValue(containerPersistenceTestDirectory("builtin-codec", "/"))
				require.NoError(t, evaluatedLazyFixture(container, operation))
			} else {
				container.Lazy = operation
			}
			result := fixture.env.attach(t, fixture.ctx, fixture.cache, fixture.srv, "_builtinContainer", container)
			rowID := persistedRowID(t, fixture.cache, result)
			encoded, err := container.EncodePersistedObject(fixture.ctx, dagql.NewPersistEncodeContext(fixture.cache, rowID, call))
			require.NoError(t, err)
			value, err := container.DecodePersistedObject(fixture.ctx, dagql.NewPersistDecodeContext(fixture.srv, rowID, call), encoded.JSON)
			require.NoError(t, err)
			restored := value.(*Container)
			if ready {
				require.JSONEq(t, string(raw), string(restored.lazyJSON))
			} else {
				require.IsType(t, operation, restored.Lazy)
				require.NotSame(t, operation, restored.Lazy)
			}
		}
		_, err = persistedContainerRecipeVisitors[call.Field](raw, newPersistedRefWalker(func(*dagql.PersistedRef) error { t.Fatal("builtin recipe declared a child"); return nil }, dagql.PersistedRefPath{}))
		require.NoError(t, err)

	})
	t.Run("exact equivalent input", func(t *testing.T) {
		original := f.recipes[0].(*DirectoryGitCleanedLazy).Repo
		local := original.Self().Backend.(*LocalGitRepository)
		other := f.env.attach(t, f.ctx, f.cache, f.srv, "equivalent-repo", &GitRepository{Backend: &LocalGitRepository{Directory: local.Directory}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
		require.NotEqual(t, persistedRowID(t, f.cache, original), persistedRowID(t, f.cache, other))
		equivalent := digest.FromString("equivalent-repository")
		require.NoError(t, f.cache.TeachContentDigest(f.ctx, original, equivalent))
		require.NoError(t, f.cache.TeachContentDigest(f.ctx, other, equivalent))
		saved := &DirectoryGitCleanedLazy{LazyState: NewLazyState(), Repo: other}
		kind, raw, err := encodePersistedDirectoryLazy(f.ctx, enc, saved)
		require.NoError(t, err)
		decoded, err := decodePersistedDirectoryLazy(f.ctx, dec, kind, raw)
		require.NoError(t, err)
		require.Equal(t, persistedRowID(t, f.cache, other), persistedRowID(t, f.cache, decoded.(*DirectoryGitCleanedLazy).Repo))
	})

	t.Run("snapshot without operation", func(t *testing.T) {
		dir := containerPersistenceTestDirectory("negative-snapshot", "/")
		res := f.env.attach(t, f.ctx, f.cache, f.srv, "negative", dir)
		rec := coreRelocationRecord(t, f.ctx, f.cache, res)
		var payload persistedDirectoryPayload
		require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &payload))
		require.Empty(t, payload.LazyKind)
		require.Empty(t, payload.LazyJSON)
		value, err := dir.DecodePersistedObject(f.ctx, dagql.NewPersistDecodeContext(f.srv, rec.ResultID, rec.Call), rec.Envelope.ObjectJSON)
		require.NoError(t, err)
		require.IsType(t, &DirectoryRestoreLazy{}, value.(*Directory).Lazy)
		require.Empty(t, value.(*Directory).lazyJSON)
	})

	_, err := decodePersistedDirectoryLazy(f.ctx, dec, "unknown", json.RawMessage(`{}`))
	require.Error(t, err)
	_, err = decodePersistedFileLazy(f.ctx, dec, "unknown", json.RawMessage(`{}`))
	require.Error(t, err)
}

func TestLazyOperationRelocation(t *testing.T) {
	f := newLazyOperationFixture(t)
	for i, recipe := range f.recipes {
		dir := containerPersistenceTestDirectory(fmt.Sprintf("output-%d", i), "/")
		require.NoError(t, evaluatedLazyFixture(dir, recipe))
		result := attachLazyOperationDirectory(t, f, dir, recipe)
		refs := assertPersistedRefsMatchOwnership(t, f.ctx, f.cache, result)
		if _, scratch := recipe.(*DirectoryScratchLazy); scratch {
			require.Empty(t, refs)
		} else {
			require.NotEmpty(t, refs)
		}
		rec := coreRelocationRecord(t, f.ctx, f.cache, result)
		mapping := map[uint64]uint64{rec.ResultID: rec.ResultID}
		for _, id := range refs {
			mapping[id] = id + 10000
		}
		visitor := &relocationVisitor{mapping: mapping}
		out, err := dagql.VisitEncodedReferences(rec, visitor.visit)
		require.NoError(t, err)
		var before, after persistedDirectoryPayload
		require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &before))
		require.NoError(t, json.Unmarshal(out.Envelope.ObjectJSON, &after))
		var a, b map[string]any
		require.NoError(t, json.Unmarshal(before.LazyJSON, &a))
		require.NoError(t, json.Unmarshal(after.LazyJSON, &b))
		for key, value := range a {
			if strings.HasSuffix(key, "ResultID") {
				require.Equal(t, value.(float64)+10000, b[key])
			} else {
				require.Equal(t, value, b[key])
			}
		}
	}
	stateA := f.env.attach(t, f.ctx, f.cache, f.srv, "http-state-a", &HTTPState{URL: "https://origin/saved"})
	stateB := f.env.attach(t, f.ctx, f.cache, f.srv, "http-state-b", &HTTPState{URL: "https://origin/saved"})
	file := storedSnapshotTestValue("File", "http-frame", "data", false).(*File)
	require.NoError(t, evaluatedLazyFixture(file, &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: "https://origin/saved", Filename: "data", BodyDigest: digest.FromString("body")}))
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "_resolve", Type: dagql.NewResultCallType(file.Type()), Receiver: &dagql.ResultCallRef{ResultID: persistedRowID(t, f.cache, stateA)}}
	result, err := f.cache.GetOrInitCall(f.ctx, f.env.session, f.srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(file, f.srv, frame)
	})
	require.NoError(t, err)
	rec := coreRelocationRecord(t, f.ctx, f.cache, result)
	visitor := &relocationVisitor{mapping: map[uint64]uint64{rec.ResultID: rec.ResultID, persistedRowID(t, f.cache, stateA): persistedRowID(t, f.cache, stateB)}}
	out, err := dagql.VisitEncodedReferences(rec, visitor.visit)
	require.NoError(t, err)
	require.Empty(t, visitor.childIDs())
	require.Equal(t, persistedRowID(t, f.cache, stateB), out.Call.Receiver.ResultID)
	require.JSONEq(t, string(rec.Envelope.ObjectJSON), string(out.Envelope.ObjectJSON))
}

func TestLazyOperationSaveReopen(t *testing.T) {
	f := newLazyOperationFixture(t)
	records := []dagql.PersistedRecord{}
	for i, recipe := range f.recipes {
		dir := containerPersistenceTestDirectory(fmt.Sprintf("saved-%d", i), "/saved")
		require.NoError(t, evaluatedLazyFixture(dir, recipe))
		res := attachLazyOperationDirectory(t, f, dir, recipe)
		rec, err := f.cache.CapturePersistedRecord(f.ctx, res)
		require.NoError(t, err)
		records = append(records, rec)
	}
	state := f.env.attach(t, f.ctx, f.cache, f.srv, "saved-state", &HTTPState{URL: "https://origin/saved"})
	httpFile := storedSnapshotTestValue("File", "saved-http", "../data", false).(*File)
	require.NoError(t, evaluatedLazyFixture(httpFile, &FileHTTPResolveLazy{LazyState: NewLazyState(), URL: "https://origin/saved", Filename: "../data", Permissions: 0600, BodyDigest: digest.FromString("saved")}))
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "_resolve", Receiver: &dagql.ResultCallRef{ResultID: persistedRowID(t, f.cache, state)}, Type: dagql.NewResultCallType(httpFile.Type())}
	httpRes, err := f.cache.GetOrInitCall(f.ctx, f.env.session, f.srv, &dagql.CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(httpFile, f.srv, call)
	})
	require.NoError(t, err)
	rec, err := f.cache.CapturePersistedRecord(f.ctx, httpRes)
	require.NoError(t, err)
	records = append(records, rec)
	schema := storedSnapshotTestValue("File", "saved-schema", "schema.json", false).(*File)
	require.NoError(t, evaluatedLazyFixture(schema, &FileBlobLazy{LazyState: NewLazyState(), Filename: "schema.json", Contents: []byte(`{"saved":true}`), Permissions: 0644}))
	schemaRes := f.env.attach(t, f.ctx, f.cache, f.srv, "saved-schema", schema)
	rec, err = f.cache.CapturePersistedRecord(f.ctx, schemaRes)
	require.NoError(t, err)
	records = append(records, rec)
	builtin := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	builtin.FS.setValue(containerPersistenceTestDirectory("saved-builtin", "/"))
	require.NoError(t, evaluatedLazyFixture(builtin, &ContainerBuiltinLazy{LazyState: NewLazyState(), Platform: Platform{OS: "linux", Architecture: "arm64"}, ManifestDigest: digest.FromString("saved-manifest")}))
	builtinRes := f.env.attach(t, f.ctx, f.cache, f.srv, "_builtinContainer", builtin)
	rec, err = f.cache.CapturePersistedRecord(f.ctx, builtinRes)
	require.NoError(t, err)
	records = append(records, rec)

	for range 2 {
		f.ctx, f.cache, f.srv = f.env.restart(t, f.ctx, f.cache)
		f.srv.InstallObject(dagql.NewClass(f.srv, dagql.ClassOpts[*GitRef]{}))
		f.srv.InstallObject(dagql.NewClass(f.srv, dagql.ClassOpts[*GitCommit]{}))
		f.srv.InstallObject(dagql.NewClass(f.srv, dagql.ClassOpts[*GitBundle]{}))
		f.srv.InstallObject(dagql.NewClass(f.srv, dagql.ClassOpts[*HTTPState]{}))
		for _, rec := range records {
			res, err := f.cache.LoadResultByResultID(f.ctx, f.env.session, f.srv, rec.ResultID)
			require.NoError(t, err)
			switch value := res.Unwrap().(type) {
			case *Directory:
				require.NotNil(t, value.Lazy)
				require.NotEmpty(t, value.lazyJSON)
				require.Zero(t, f.env.manager.openCount(value.stored.SnapshotID))
			case *File:
				require.NotNil(t, value.Lazy)
				require.NotEmpty(t, value.lazyJSON)
				require.Zero(t, f.env.manager.openCount(value.stored.SnapshotID))
			case *Container:
				require.NotNil(t, value.Lazy)
				require.NotEmpty(t, value.lazyJSON)
				require.Zero(t, f.env.manager.openCount("saved-builtin"))
			default:
				t.Fatalf("unexpected output %T", value)
			}
			captured, err := f.cache.CapturePersistedRecord(f.ctx, res)
			require.NoError(t, err)
			require.JSONEq(t, string(rec.Envelope.ObjectJSON), string(captured.Envelope.ObjectJSON))
		}
		for _, input := range f.inputs {
			inputID, _ := input.ID()
			id := inputID.EngineResultID()
			for _, row := range f.cache.DebugEGraphSnapshot().Results {
				if row.SharedResultID == id {
					require.False(t, row.HasValue, "input decoded during capture")
				}
			}
		}
	}
	const holder = "eager-operations-holder"
	for _, record := range records {
		_, err := f.cache.LoadResultByResultID(f.ctx, holder, f.srv, record.ResultID)
		require.NoError(t, err)
	}
	require.NoError(t, f.cache.ReleaseSession(f.ctx, f.env.session))
	for _, input := range f.inputs {
		id, err := input.ID()
		require.NoError(t, err)
		retained, err := f.cache.LoadResultByResultID(f.ctx, holder, f.srv, id.EngineResultID())
		require.NoError(t, err)
		require.Equal(t, id.EngineResultID(), persistedRowID(t, f.cache, retained))
	}
	require.NoError(t, f.cache.ReleaseSession(f.ctx, holder))
	_, err = f.cache.Prune(f.ctx, []dagql.CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	for _, record := range records {
		_, err := f.cache.LoadResultByResultID(f.ctx, f.env.session, f.srv, record.ResultID)
		require.Error(t, err, "pruned operation retained its row")
	}
	for _, input := range f.inputs {
		id, err := input.ID()
		require.NoError(t, err)
		_, err = f.cache.LoadResultByResultID(f.ctx, holder, f.srv, id.EngineResultID())
		require.Error(t, err, "pruned operation retained an input")
	}
}
