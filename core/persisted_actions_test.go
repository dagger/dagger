package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// TestPersistedArtifactsShareOwnersThroughRestart verifies that artifact
// selections, bound addresses, SDKs and module values retain the same workspace
// and node references through two cache restarts, without evaluating artifacts.
func TestPersistedArtifactsShareOwnersThroughRestart(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "artifacts-a")
	ctx, cache, srv := env.open(t)
	modRes := env.attach(t, ctx, cache, srv, "cold-module", &Module{NameField: "cold", OriginalName: "cold", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	modID := persistedRowID(t, cache, modRes)
	dirRes := env.directory(t, ctx, cache, srv, "shared-root", "shared-root")
	dirID := persistedRowID(t, cache, dirRes)
	wsRes := env.attach(t, ctx, cache, srv, "bound-workspace", &Workspace{rootfs: dirRes, Address: "file:///ws", Cwd: "/"}).(dagql.ObjectResult[*Workspace])
	wsID := persistedRowID(t, cache, wsRes)
	root := &ModTreeNode{Name: "cold", Module: modRes, OriginalModule: modRes, RootValue: dirRes}
	entries := []*Artifact{
		{Path: []string{"cold", "lint"}, TypeName: "Check", DimensionKeys: []*ArtifactDimensionKey{}, Directives: []string{"check"}, Workspace: wsRes, Node: &ModTreeNode{Name: "lint", Description: "lints", Parent: root, Module: modRes, Directives: []string{"check"}}},
		{Path: []string{"cold", "format"}, TypeName: "Changeset", DimensionKeys: []*ArtifactDimensionKey{}, Directives: []string{"generate"}, Workspace: wsRes, Node: &ModTreeNode{Name: "format", Parent: root, Module: modRes, Directives: []string{"generate"}}},
	}
	artifacts := env.attach(t, ctx, cache, srv, "artifacts", &Artifacts{Entries: entries})
	single := env.attach(t, ctx, cache, srv, "artifact", entries[0])
	address := env.attach(t, ctx, cache, srv, "address", &Address{Value: "dag://cold/lint", BoundWorkspace: wsRes})
	sdk := env.attach(t, ctx, cache, srv, "sdk", &WorkspaceSDK{Name: "test", Workspace: wsRes})
	holderDef := NewObjectTypeDef("Holder", "", nil)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: modRes, TypeDef: holderDef}}))
	holder := env.attach(t, ctx, cache, srv, "holder", &ModuleObject{Module: modRes, TypeDef: holderDef, Fields: map[string]any{"dir": dirRes, "label": "x"}})
	ids := map[string]uint64{}
	encodings := map[string]dagql.PersistedResultEnvelope{}
	for name, res := range map[string]dagql.AnyResult{"artifacts": artifacts, "artifact": single, "address": address, "sdk": sdk, "holder": holder} {
		ids[name] = persistedRowID(t, cache, res)
		encodings[name] = persistedEncoding(t, ctx, cache, res).Envelope
		require.NotEmpty(t, assertPersistedRefsMatchOwnership(t, ctx, cache, res), name)
	}
	refs := persistedVisitedRefs(t, ctx, cache, artifacts)
	require.Equal(t, wsID, refs["objectJSON.Entries[0].Workspace"])
	require.Equal(t, modID, refs["objectJSON.Tree.nodes[0].moduleResultID"])
	require.Equal(t, dirID, refs["objectJSON.Tree.nodes[0].rootValueResultID"])
	snapshotOwners := func(snapshotID string) int {
		env.manager.mu.Lock()
		defer env.manager.mu.Unlock()
		owners := 0
		for _, id := range env.manager.owners {
			if id == snapshotID {
				owners++
			}
		}
		return owners
	}
	require.Equal(t, 1, snapshotOwners("shared-root"))
	for round := 1; round <= 2; round++ {
		ctx, cache, srv = env.restart(t, ctx, cache)
		restartedMod, err := cache.LoadResultByResultID(ctx, env.session, srv, modID)
		require.NoError(t, err)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: restartedMod.(dagql.ObjectResult[*Module]), TypeDef: holderDef}}))
		load := func(name string) dagql.AnyResult {
			res, err := cache.LoadResultByResultID(ctx, env.session, srv, ids[name])
			require.NoError(t, err, name)
			require.Equal(t, encodings[name], persistedEncoding(t, ctx, cache, res).Envelope, "round %d: %s", round, name)
			return res
		}
		restored := load("artifacts").Unwrap().(*Artifacts)
		require.Len(t, restored.Entries, 2)
		require.Equal(t, "lint", restored.Entries[0].Node.Name)
		require.Equal(t, []string{"check"}, restored.Entries[0].Directives)
		require.Equal(t, []string{"generate"}, restored.Entries[1].Node.Directives)
		require.Same(t, restored.Entries[0].Node.Parent, restored.Entries[1].Node.Parent)
		require.Equal(t, wsID, persistedRowID(t, cache, restored.Entries[0].Workspace))
		require.Equal(t, dirID, persistedRowID(t, cache, restored.Entries[0].Node.Parent.RootValue))
		require.Equal(t, "cold", load("artifact").Unwrap().(*Artifact).Node.Parent.Name)
		require.Equal(t, wsID, persistedRowID(t, cache, load("address").Unwrap().(*Address).BoundWorkspace))
		require.Equal(t, wsID, persistedRowID(t, cache, load("sdk").Unwrap().(*WorkspaceSDK).Workspace))
		restoredHolder := load("holder").Unwrap().(*ModuleObject)
		require.Equal(t, dirID, persistedRowID(t, cache, restoredHolder.Fields["dir"].(dagql.AnyResult)))
		require.Equal(t, "x", restoredHolder.Fields["label"])
		require.Equal(t, 1, snapshotOwners("shared-root"))
	}
	// Final removal: with no session holding the rows, prune both retained
	// owners and every other root; the shared Directory's snapshot lease is
	// removed exactly once.
	require.NoError(t, cache.ReleaseSession(ctx, env.session))
	env.manager.mu.Lock()
	removedBefore := len(env.manager.removeCalls)
	env.manager.mu.Unlock()
	_, err := cache.Prune(ctx, []dagql.CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	require.Zero(t, snapshotOwners("shared-root"), "no lease is left on the shared snapshot after both owners are removed")
	env.manager.mu.Lock()
	removedLeases := map[string]int{}
	for _, leaseID := range env.manager.removeCalls[removedBefore:] {
		removedLeases[leaseID]++
	}
	env.manager.mu.Unlock()
	require.NotEmpty(t, removedLeases, "pruning removed leases")
	for leaseID, n := range removedLeases {
		require.Equal(t, 1, n, "lease %s is released exactly once", leaseID)
	}
	_, err = cache.LoadResultByResultID(ctx, env.session, srv, dirID)
	require.Error(t, err, "the shared Directory row is gone once its last owner is removed")
}

// TestPersistedModuleEnumDecodesThroughDefiningModule saves a module-defined
// enum value whose recorded call names its module, restarts on a server that
// does not define the enum, and decodes it through the defining module's
// server resolved from that saved module row.
func TestPersistedModuleEnumDecodesThroughDefiningModule(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "enum-a")
	ctx, cache, srv := env.open(t)

	member := env.attach(t, ctx, cache, srv, "enum-member-a", NewEnumMemberTypeDef("ALPHA", "ALPHA", "", nil, dagql.ObjectResult[*SourceMap]{})).(dagql.ObjectResult[*EnumMemberTypeDef])
	other := env.attach(t, ctx, cache, srv, "enum-member-b", NewEnumMemberTypeDef("BETA", "BETA", "", nil, dagql.ObjectResult[*SourceMap]{})).(dagql.ObjectResult[*EnumMemberTypeDef])
	enumDef := NewEnumTypeDef("ColdKind", "kinds", dagql.ObjectResult[*SourceMap]{})
	enumDef.Members = dagql.ObjectResultArray[*EnumMemberTypeDef]{member, other}
	enumDefRes := env.attach(t, ctx, cache, srv, "enum-def", enumDef).(dagql.ObjectResult[*EnumTypeDef])
	typeDefRes := env.attach(t, ctx, cache, srv, "enum-typedef", (&TypeDef{}).WithEnum(enumDefRes)).(dagql.ObjectResult[*TypeDef])
	modRes := env.attach(t, ctx, cache, srv, "enum-module", &Module{NameField: "cold", OriginalName: "cold", Deps: NewSchemaBuilder(nil, nil), EnumDefs: dagql.ObjectResultArray[*TypeDef]{typeDefRes}}).(dagql.ObjectResult[*Module])
	modID := persistedRowID(t, cache, modRes)

	value := &ModuleEnum{TypeDef: enumDef, Name: "BETA"}
	frame := &dagql.ResultCall{
		Kind:   dagql.ResultCallKindField,
		Field:  "kind",
		Type:   dagql.NewResultCallType(value.Type()),
		Module: &dagql.ResultCallModule{Name: "cold", ResultRef: &dagql.ResultCallRef{ResultID: modID}},
	}
	valueRes, err := cache.GetOrInitCall(ctx, env.session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(value, frame)
	})
	require.NoError(t, err)
	valueID := persistedRowID(t, cache, valueRes)
	encoding := persistedEncoding(t, ctx, cache, valueRes)
	require.Equal(t, "scalar_json", encoding.Envelope.Kind)
	require.Equal(t, "ColdKind", encoding.Envelope.TypeName)
	require.JSONEq(t, `"BETA"`, string(encoding.Envelope.ScalarJSON))

	ctx, cache, srv = env.restart(t, ctx, cache)
	_, defined := srv.ScalarType("ColdKind")
	require.False(t, defined, "the reading server does not define the module enum")
	_, err = cache.LoadResultByResultID(ctx, env.session, srv, valueID)
	require.ErrorContains(t, err, "unknown scalar type", "without a defining-server resolver the cold enum cannot decode")

	var resolvedFor []string
	srv.SetResultServerForCall(func(ctx context.Context, call *dagql.ResultCall) (*dagql.Server, error) {
		resolvedFor = append(resolvedFor, call.Field)
		if call.Module == nil || call.Module.ResultRef == nil {
			return srv, nil
		}
		modAny, err := cache.LoadResultByResultID(ctx, env.session, srv, call.Module.ResultRef.ResultID)
		if err != nil {
			return nil, err
		}
		return dagqlServerForModule(ctx, modAny.(dagql.ObjectResult[*Module]))
	})
	restored, err := cache.LoadResultByResultID(ctx, env.session, srv, valueID)
	require.NoError(t, err)
	require.Equal(t, []string{"kind"}, resolvedFor, "the defining module is resolved from the recorded call")
	restoredEnum, ok := restored.Unwrap().(*ModuleEnum)
	require.True(t, ok, "decoded through the module's installed enum, got %T", restored.Unwrap())
	require.Equal(t, "BETA", restoredEnum.Name)
	require.Equal(t, "ColdKind", restoredEnum.TypeName())
	require.Equal(t, value.Type().String(), restored.Type().String())
	require.Equal(t, encoding.Envelope, persistedEncoding(t, ctx, cache, restored).Envelope, "the second save is identical")
}
