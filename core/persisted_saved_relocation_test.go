package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

const (
	// The first integer float64 cannot hold, and the signed 64-bit floor.
	savedBigInt   = "9007199254740993"
	savedMinInt64 = "-9223372036854775808"
)

// TestPersistedRelocatedRowsSurviveWorkerSaveAndReopen carries relocated
// payloads through the actual save path rather than through a direct
// re-encode. The relocated values are attached as ordinary rows on a
// path-backed store, then the store is closed and reopened three times:
//
//   - the first reopen follows a save of the live relocated values;
//   - the middle process never reads either row and never installs the module
//     object class, so its save can only copy the bytes already on disk;
//   - the last process reads both rows typed, checks the B references, the
//     opaque numbers and the SDK conversion, and saves once more.
//
// The middle process is what makes this more than a codec round trip: if the
// untouched save decoded the module object to re-encode it, that process would
// fail, because it has no class to decode it with.
func TestPersistedRelocatedRowsSurviveWorkerSaveAndReopen(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "reloc-saved")
	ctx, cache, srv := env.open(t)

	dirA := env.directory(t, ctx, cache, srv, "saved-dir-a", "saved-snap-a")
	dirB := env.directory(t, ctx, cache, srv, "saved-dir-b", "saved-snap-b")
	fileA := attachStoredSnapshotTestValue(t, ctx, cache, srv, env.session, "saved-file-a", storedSnapshotTestValue("File", "saved-file-a", "/a.txt", false), true).(dagql.ObjectResult[*File])
	fileB := attachStoredSnapshotTestValue(t, ctx, cache, srv, env.session, "saved-file-b", storedSnapshotTestValue("File", "saved-file-b", "/b.txt", false), true).(dagql.ObjectResult[*File])
	handleA := env.directory(t, ctx, cache, srv, "saved-handle-a", "saved-handle-snap-a")
	handleB := env.directory(t, ctx, cache, srv, "saved-handle-b", "saved-handle-snap-b")
	// The module and its source carry the untyped configuration a module is
	// handed back after a restart, so their numbers travel this path too.
	nestedConfig := func() map[string]any {
		return map[string]any{
			"limits": map[string]any{"max": json.Number(savedBigInt), "floor": json.Number(savedMinInt64)},
			"ports":  []any{json.Number("8080"), json.Number(savedBigInt)},
			"ratio":  json.Number("0.1"),
		}
	}
	modRes := env.attach(t, ctx, cache, srv, "saved-module", &Module{
		NameField:       "saved",
		OriginalName:    "saved",
		Deps:            NewSchemaBuilder(nil, nil),
		SDKConfig:       &SDKConfig{Source: "go", Config: nestedConfig()},
		WorkspaceConfig: nestedConfig(),
	}).(dagql.ObjectResult[*Module])
	srcRes := env.attach(t, ctx, cache, srv, "saved-module-source", &ModuleSource{
		SDK:     &SDKConfig{Source: "go", Config: nestedConfig()},
		SDKImpl: &moduleSourceSelfCallsTestSDK{},
	}).(dagql.ObjectResult[*ModuleSource])
	ids := map[string]uint64{
		"dirA": persistedRowID(t, cache, dirA), "dirB": persistedRowID(t, cache, dirB),
		"fileA": persistedRowID(t, cache, fileA), "fileB": persistedRowID(t, cache, fileB),
		"handleA": persistedRowID(t, cache, handleA), "handleB": persistedRowID(t, cache, handleB),
		"module": persistedRowID(t, cache, modRes), "source": persistedRowID(t, cache, srcRes),
	}

	holderDef := NewObjectTypeDef("SavedHolder", "", nil)
	installHolder := func(srv *dagql.Server, mod dagql.ObjectResult[*Module]) {
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: mod, TypeDef: holderDef}}))
	}
	installHolder(srv, modRes)

	// The A-side values, as a producing engine would hold them.
	lazyA := &Directory{
		Platform: Platform{OS: "linux", Architecture: "arm64"},
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	lazyA.Dir.setValue("/")
	lazyA.Lazy = &DirectoryWithFileLazy{LazyState: NewLazyState(), Parent: dirA, Source: fileA, DestPath: "/a.txt"}
	lazyARes := env.attach(t, ctx, cache, srv, "saved-lazy-a", lazyA)

	handleAID, err := handleA.ID()
	require.NoError(t, err)
	const bigInt = savedBigInt
	holderA := &ModuleObject{Module: modRes, TypeDef: holderDef, Fields: map[string]any{
		"child": dirA,
		"state": map[string]any{
			"handle": handleAID,
			"counts": []any{json.Number(bigInt), json.Number(savedMinInt64)},
			"label":  "9007199254740993",
		},
	}}
	holderARes := env.attach(t, ctx, cache, srv, "saved-holder-a", holderA)

	// Relocate both records onto the B rows and decode the rewritten bytes.
	mapping := map[uint64]uint64{
		ids["dirA"]: ids["dirB"], ids["dirB"]: ids["dirA"],
		ids["fileA"]: ids["fileB"], ids["fileB"]: ids["fileA"],
		ids["handleA"]: ids["handleB"], ids["handleB"]: ids["handleA"],
		ids["module"]: ids["module"],
	}
	relocateAndDecode := func(res dagql.AnyResult, receiver PersistedObjectDecoderFunc) dagql.Typed {
		t.Helper()
		rec := coreRelocationRecord(t, ctx, cache, res)
		visitor := &relocationVisitor{mapping: map[uint64]uint64{}}
		for from, to := range mapping {
			visitor.mapping[from] = to
		}
		visitor.mapping[rec.ResultID] = rec.ResultID
		out, err := dagql.VisitEncodedReferences(rec, visitor.visit)
		require.NoError(t, err)
		require.NotEmpty(t, visitor.childIDs(), "the payload declares references to relocate")
		decoded, err := receiver(dagql.NewPersistDecodeContext(srv, out.ResultID, out.Call), out.Envelope.ObjectJSON)
		require.NoError(t, err)
		return decoded
	}
	relocatedLazy := relocateAndDecode(lazyARes, func(dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
		return (&Directory{}).DecodePersistedObject(ctx, dec, payload)
	}).(*Directory)
	relocatedHolder := relocateAndDecode(holderARes, func(dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
		return holderA.DecodePersistedObject(ctx, dec, payload)
	}).(*ModuleObject)

	// Attach the relocated values as ordinary rows under their own calls, so
	// the save path handles them like any other retained result.
	relocatedLazyRes := env.attach(t, ctx, cache, srv, "saved-lazy-relocated", relocatedLazy)
	relocatedHolderRes := env.attach(t, ctx, cache, srv, "saved-holder-relocated", relocatedHolder)
	lazyID := persistedRowID(t, cache, relocatedLazyRes)
	holderID := persistedRowID(t, cache, relocatedHolderRes)
	savedLazyEnvelope := persistedEncoding(t, ctx, cache, relocatedLazyRes).Envelope
	savedHolderEnvelope := persistedEncoding(t, ctx, cache, relocatedHolderRes).Envelope

	assertNestedConfig := func(t *testing.T, label string, cfg map[string]any) {
		t.Helper()
		limits, ok := cfg["limits"].(map[string]any)
		require.True(t, ok, label)
		require.Equal(t, json.Number(savedBigInt), limits["max"], "%s: the nested large integer is exact after the reopen", label)
		require.Equal(t, json.Number(savedMinInt64), limits["floor"], label)
		ports, ok := cfg["ports"].([]any)
		require.True(t, ok, label)
		require.Equal(t, json.Number(savedBigInt), ports[1], "%s: a list position is exact too", label)
		require.Equal(t, json.Number("0.1"), cfg["ratio"], label)
	}

	assertLoaded := func(t *testing.T, round string, cache *dagql.Cache, srv *dagql.Server, restoredMod dagql.ObjectResult[*Module]) {
		t.Helper()

		// The module and source configuration a restored module is handed.
		require.NotNil(t, restoredMod.Self().SDKConfig, round)
		assertNestedConfig(t, round+": module sdk config", restoredMod.Self().SDKConfig.Config)
		assertNestedConfig(t, round+": module workspace config", restoredMod.Self().WorkspaceConfig)
		loadedSrc, err := cache.LoadResultByResultID(ctx, env.session, srv, ids["source"])
		require.NoError(t, err, round)
		src, ok := loadedSrc.Unwrap().(*ModuleSource)
		require.True(t, ok)
		require.NotNil(t, src.SDK, round)
		assertNestedConfig(t, round+": module source sdk config", src.SDK.Config)
		loadedLazy, err := cache.LoadResultByResultID(ctx, env.session, srv, lazyID)
		require.NoError(t, err, round)
		dir, ok := loadedLazy.Unwrap().(*Directory)
		require.True(t, ok)
		lazy, ok := dir.Lazy.(*DirectoryWithFileLazy)
		require.True(t, ok, "%s: the lazy kind survived the save", round)
		require.Equal(t, ids["dirB"], persistedRowID(t, cache, lazy.Parent), round)
		parentSnapshot, ok := lazy.Parent.Self().snapshotIdentity()
		require.True(t, ok)
		require.Equal(t, "saved-snap-b", parentSnapshot, "%s: the parent is the B row's actual content", round)
		require.Equal(t, ids["fileB"], persistedRowID(t, cache, lazy.Source), round)
		sourceSnapshot, ok := lazy.Source.Self().snapshotIdentity()
		require.True(t, ok)
		require.Equal(t, "saved-file-b", sourceSnapshot, round)
		require.Equal(t, "/a.txt", lazy.DestPath, round)
		require.Equal(t, savedLazyEnvelope, persistedEncoding(t, ctx, cache, loadedLazy).Envelope, "%s: the typed read re-encodes to the same bytes", round)

		loadedHolder, err := cache.LoadResultByResultID(ctx, env.session, srv, holderID)
		require.NoError(t, err, round)
		holder, ok := loadedHolder.Unwrap().(*ModuleObject)
		require.True(t, ok)
		child, ok := holder.Fields["child"].(dagql.AnyResult)
		require.True(t, ok, round)
		require.Equal(t, ids["dirB"], persistedRowID(t, cache, child), round)
		childSnapshot, ok := child.Unwrap().(*Directory).snapshotIdentity()
		require.True(t, ok)
		require.Equal(t, "saved-snap-b", childSnapshot, "%s: the module object's child is the B row's content", round)
		state, ok := holder.Fields["state"].(map[string]any)
		require.True(t, ok, round)
		require.Equal(t, json.Number(bigInt), state["counts"].([]any)[0], "%s: numbers stay exact through the save", round)
		require.Equal(t, json.Number(savedMinInt64), state["counts"].([]any)[1], round)
		require.Equal(t, "9007199254740993", state["label"], "%s: a numeric-looking string is unchanged", round)
		// Attaching the relocated object resolved its private handle into an
		// owned child, which is what attachment does with a handle naming a
		// live row. The saved field is therefore a row reference, and it is
		// the B row.
		handleChild, ok := state["handle"].(dagql.AnyResult)
		require.True(t, ok, "%s: the private handle became an owned child, got %T", round, state["handle"])
		require.Equal(t, ids["handleB"], persistedRowID(t, cache, handleChild), "%s: the handle names the B row", round)
		handleSnapshot, ok := handleChild.Unwrap().(*Directory).snapshotIdentity()
		require.True(t, ok)
		require.Equal(t, "saved-handle-snap-b", handleSnapshot, "%s: with the B row's actual content", round)
		require.Equal(t, savedHolderEnvelope, persistedEncoding(t, ctx, cache, loadedHolder).Envelope, "%s: the typed read re-encodes to the same bytes", round)

		// SDK conversion of the value the module gets back after the reload.
		converted, err := (&ModuleObjectType{typeDef: holderDef, mod: restoredMod}).ConvertToSDKInput(ctx, holder)
		require.NoError(t, err, round)
		fields, ok := converted.(map[string]any)
		require.True(t, ok, round)
		convertedState, ok := fields["state"].(map[string]any)
		require.True(t, ok, round)
		convertedNumbers, err := json.Marshal(map[string]any{"counts": convertedState["counts"], "label": convertedState["label"]})
		require.NoError(t, err)
		// Exact bytes, not require.JSONEq: that helper reads both sides with
		// an ordinary json.Unmarshal, which rounds these integers through
		// float64 and would compare 9007199254740993 equal to ...992.
		require.Equal(t, `{"counts":[`+savedBigInt+`,`+savedMinInt64+`],"label":"9007199254740993"}`, string(convertedNumbers),
			"%s: SDK input keeps the exact numbers", round)
		convertedHandle, ok := convertedState["handle"].(string)
		require.True(t, ok, "%s: the child is handed to the SDK as an ID", round)
		sdkHandle := &call.ID{}
		require.NoError(t, sdkHandle.Decode(convertedHandle))
		require.Equal(t, ids["handleB"], sdkHandle.EngineResultID(), "%s: the SDK sees the relocated row", round)
		require.Equal(t, handleAID.Type().ToAST().String(), sdkHandle.Type().ToAST().String(), "%s: with the same recursive type", round)
	}

	// First save of the live relocated values, then read them back typed.
	ctx, cache, srv = env.restart(t, ctx, cache)
	restoredMod, err := cache.LoadResultByResultID(ctx, env.session, srv, ids["module"])
	require.NoError(t, err)
	installHolder(srv, restoredMod.(dagql.ObjectResult[*Module]))
	assertLoaded(t, "typed read after first save", cache, srv, restoredMod.(dagql.ObjectResult[*Module]))

	// Middle process: no read, and deliberately no module object class, so a
	// save that had to decode could not succeed. Its save copies the stored
	// bytes.
	ctx, cache, srv = env.restart(t, ctx, cache)
	_, hasHolderClass := srv.ObjectType("SavedHolder")
	require.False(t, hasHolderClass, "the middle process cannot decode a module object")

	// Last process: the rows came through the untouched copy and still carry
	// the relocated references and data.
	ctx, cache, srv = env.restart(t, ctx, cache)
	restoredMod, err = cache.LoadResultByResultID(ctx, env.session, srv, ids["module"])
	require.NoError(t, err)
	installHolder(srv, restoredMod.(dagql.ObjectResult[*Module]))
	assertLoaded(t, "typed read after the untouched middle process", cache, srv, restoredMod.(dagql.ObjectResult[*Module]))

	// One more save, this time from the typed-read state, and one more read.
	ctx, cache, srv = env.restart(t, ctx, cache)
	restoredMod, err = cache.LoadResultByResultID(ctx, env.session, srv, ids["module"])
	require.NoError(t, err)
	installHolder(srv, restoredMod.(dagql.ObjectResult[*Module]))
	assertLoaded(t, "typed read after a second typed save", cache, srv, restoredMod.(dagql.ObjectResult[*Module]))
}

// PersistedObjectDecoderFunc decodes one payload for the relocation fixture.
type PersistedObjectDecoderFunc func(*dagql.PersistDecodeContext, json.RawMessage) (dagql.Typed, error)

// TestPersistedContainerRecipeRelocationUsesRecordedCall covers the one
// payload whose recipe is selected by the recorded call's field rather than by
// a kind stored in its own bytes. Directory and File name their lazy kind in
// the payload; a pending Container does not, so both its decoder and its
// reference visitor read the field off the recorded call. One pending recipe
// is enough to exercise that branch: the point is the dispatch, not the
// seventy-odd recipes behind it.
func TestPersistedContainerRecipeRelocationUsesRecordedCall(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "reloc-ctr")
	ctx, cache, srv := env.open(t)

	platform := Platform{OS: "linux", Architecture: "amd64"}
	parentA := env.attach(t, ctx, cache, srv, "ctr-parent-a", NewContainer(platform)).(dagql.ObjectResult[*Container])
	parentB := env.attach(t, ctx, cache, srv, "ctr-parent-b", NewContainer(Platform{OS: "linux", Architecture: "arm64"})).(dagql.ObjectResult[*Container])
	modA := env.attach(t, ctx, cache, srv, "ctr-module-a", &Module{NameField: "mod-a", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	modB := env.attach(t, ctx, cache, srv, "ctr-module-b", &Module{NameField: "mod-b", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	ids := map[string]uint64{
		"parentA": persistedRowID(t, cache, parentA), "parentB": persistedRowID(t, cache, parentB),
		"modA": persistedRowID(t, cache, modA), "modB": persistedRowID(t, cache, modB),
	}

	pending := NewContainer(platform)
	pending.Lazy = &ContainerExecLazy{State: &ContainerExecState{
		LazyState:     NewLazyState(),
		Parent:        parentA,
		ModuleContext: modA,
		Opts:          ContainerExecOpts{Args: []string{"echo", "hi"}},
	}}
	// The row is recorded under the real field name, which is what selects the
	// recipe on the way back in.
	res := env.attach(t, ctx, cache, srv, "withExec", pending)
	rec := coreRelocationRecord(t, ctx, cache, res)
	require.NotNil(t, rec.Call)
	require.Equal(t, "withExec", rec.Call.Field, "the recorded call names the recipe")
	before := recordJSON(t, rec)

	reloc := &relocationVisitor{mapping: map[uint64]uint64{
		rec.ResultID:   rec.ResultID,
		ids["parentA"]: ids["parentB"], ids["parentB"]: ids["parentA"],
		ids["modA"]: ids["modB"], ids["modB"]: ids["modA"],
	}}
	out, err := dagql.VisitEncodedReferences(rec, reloc.visit)
	require.NoError(t, err)
	require.Equal(t, map[string]uint64{
		"objectJSON.lazyJSON.parentResultID":        ids["parentA"],
		"objectJSON.lazyJSON.moduleContextResultID": ids["modA"],
	}, reloc.childIDs(), "the recipe's own parent and module references are reached")
	require.Equal(t, before, recordJSON(t, rec), "the input record is untouched")

	decodedTyped, err := (&Container{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, out.ResultID, out.Call), out.Envelope.ObjectJSON)
	require.NoError(t, err)
	decoded, ok := decodedTyped.(*Container)
	require.True(t, ok)
	recipe, ok := decoded.lazyOpForRouting().(*ContainerExecLazy)
	require.True(t, ok, "the recorded call selected the exec recipe")
	require.Equal(t, ids["parentB"], persistedRowID(t, cache, recipe.State.Parent))
	require.Equal(t, Platform{OS: "linux", Architecture: "arm64"}, recipe.State.Parent.Self().Platform, "the parent is the B row's actual content")
	require.Equal(t, ids["modB"], persistedRowID(t, cache, recipe.State.ModuleContext))
	require.Equal(t, "mod-b", recipe.State.ModuleContext.Self().NameField)
	require.Equal(t, []string{"echo", "hi"}, recipe.State.Opts.Args, "plain recipe data is unchanged")

	reencoded, err := decoded.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, out.ResultID, out.Call))
	require.NoError(t, err)
	require.Equal(t, string(out.Envelope.ObjectJSON), string(reencoded.JSON), "the typed save matches the relocated bytes")

	t.Run("a pending recipe without its recorded call is refused", func(t *testing.T) {
		orphan := rec
		orphan.Call = nil
		_, err := dagql.VisitEncodedReferences(orphan, reloc.visit)
		require.ErrorContains(t, err, "pending container recipe has no recorded call")
	})
	t.Run("a recorded call naming no recipe is refused", func(t *testing.T) {
		wrong := rec
		wrong.Call = &dagql.ResultCall{Kind: rec.Call.Kind, Field: "envVariable", Type: rec.Call.Type}
		_, err := dagql.VisitEncodedReferences(wrong, reloc.visit)
		require.ErrorContains(t, err, `container lazy kind "envVariable" has no reference visitor`)
	})
	t.Run("a different recipe's call does not reach these positions", func(t *testing.T) {
		other := rec
		other.Call = &dagql.ResultCall{Kind: rec.Call.Kind, Field: "withNewFile", Type: rec.Call.Type}
		otherReloc := &relocationVisitor{mapping: reloc.mapping}
		_, err := dagql.VisitEncodedReferences(other, otherReloc.visit)
		require.NoError(t, err, "the payload still parses under another recipe's shape")
		require.Equal(t, map[string]uint64{"objectJSON.lazyJSON.parentResultID": ids["parentA"]}, otherReloc.childIDs(),
			"only the position that recipe declares is reached; the exec's module context is not")
	})
}
