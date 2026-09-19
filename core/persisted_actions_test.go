package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// TestPersistedActionGroupsShareOwnersThroughRestart saves check, up and
// terminal groups whose bound workspace is rooted at one Directory, together
// with a module object that holds the same Directory as a private field. After
// restart every group restores its exact workspace, node table and error row,
// the shared Directory is one row with one snapshot owner, and removing both
// retained owners releases it once. Decoding restores data and references
// only; no action runs.
func TestPersistedActionGroupsShareOwnersThroughRestart(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "actions-a")
	ctx, cache, srv := env.open(t)

	modRes := env.attach(t, ctx, cache, srv, "cold-module", &Module{NameField: "cold", OriginalName: "cold", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	modID := persistedRowID(t, cache, modRes)
	dirRes := env.directory(t, ctx, cache, srv, "shared-root", "shared-root")
	dirID := persistedRowID(t, cache, dirRes)
	wsRes := env.attach(t, ctx, cache, srv, "bound-workspace", &Workspace{rootfs: dirRes, Address: "file:///ws", Cwd: "/"}).(dagql.ObjectResult[*Workspace])
	wsID := persistedRowID(t, cache, wsRes)
	errRes := env.attach(t, ctx, cache, srv, "check-error", &Error{Message: "boom"}).(dagql.ObjectResult[*Error])
	errID := persistedRowID(t, cache, errRes)

	root := &ModTreeNode{Name: "cold", Module: modRes, OriginalModule: modRes}
	lint := &ModTreeNode{Name: "lint", Description: "lints", Parent: root, Module: modRes, IsCheck: true}
	format := &ModTreeNode{Name: "format", Parent: root, Module: modRes, IsCheck: true, IsGenerator: true}
	web := &ModTreeNode{Name: "web", Parent: root, Module: modRes, IsUp: true}
	shell := &ModTreeNode{Name: "shell", Parent: root, Module: modRes}
	frontend := 8080

	failed := &Check{Node: lint, Completed: true, Passed: false, Error: dagql.NonNull(errRes)}
	pending := &Check{Node: format, IsGenerate: true}
	checkGroup := env.attach(t, ctx, cache, srv, "check-group", &CheckGroup{Node: root, Checks: []*Check{failed, pending}, BoundWorkspace: wsRes})
	upGroup := env.attach(t, ctx, cache, srv, "up-group", &UpGroup{Node: root, Ups: []*Up{{Node: web, PortMappings: []PortForward{{Frontend: &frontend, Backend: 80, Protocol: NetworkProtocolTCP}}}}, BoundWorkspace: wsRes})
	terminalGroup := env.attach(t, ctx, cache, srv, "terminal-group", &TerminalGroup{Node: root, Terminals: []*TerminalTarget{{Node: shell}}, BoundWorkspace: wsRes})
	singleCheck := env.attach(t, ctx, cache, srv, "single-check", &Check{Node: lint, Completed: true, Passed: false, Error: dagql.NonNull(errRes)})
	singleUp := env.attach(t, ctx, cache, srv, "single-up", &Up{Node: web})
	singleTerminal := env.attach(t, ctx, cache, srv, "single-terminal", &TerminalTarget{Node: shell})

	holderDef := NewObjectTypeDef("Holder", "", nil)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: modRes, TypeDef: holderDef}}))
	holder := env.attach(t, ctx, cache, srv, "holder", &ModuleObject{Module: modRes, TypeDef: holderDef, Fields: map[string]any{"dir": dirRes, "label": "x"}})

	ids := map[string]uint64{}
	encodings := map[string]dagql.PersistedResultEnvelope{}
	for name, res := range map[string]dagql.AnyResult{
		"check-group": checkGroup, "up-group": upGroup, "terminal-group": terminalGroup,
		"single-check": singleCheck, "single-up": singleUp, "single-terminal": singleTerminal, "holder": holder,
	} {
		ids[name] = persistedRowID(t, cache, res)
		encodings[name] = persistedEncoding(t, ctx, cache, res).Envelope
		refs := assertPersistedRefsMatchOwnership(t, ctx, cache, res)
		require.NotEmpty(t, refs, "%s declares its owned rows", name)
	}
	groupRefs := persistedVisitedRefs(t, ctx, cache, checkGroup)
	require.Equal(t, wsID, groupRefs["objectJSON.boundWorkspaceResultID"], "the bound workspace is a declared reference")
	require.Equal(t, errID, groupRefs["objectJSON.checks[0].errorResultID"])
	require.Equal(t, modID, groupRefs["objectJSON.tree.nodes[0].moduleResultID"])
	require.Equal(t, modID, groupRefs["objectJSON.tree.nodes[0].originalModuleResultID"])
	require.Equal(t, dirID, persistedVisitedRefs(t, ctx, cache, holder)["objectJSON.fields.dir.resultID"], "the module object's private field names the shared Directory row")

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
	require.Equal(t, 1, snapshotOwners("shared-root"), "one Directory row owns the snapshot however many rows reference it")

	for round := 1; round <= 2; round++ {
		ctx, cache, srv = env.restart(t, ctx, cache)
		// A served module installs its object classes with the module row
		// they belong to; the restarted server does the same from the saved
		// module row.
		restartedMod, err := cache.LoadResultByResultID(ctx, env.session, srv, modID)
		require.NoError(t, err)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: restartedMod.(dagql.ObjectResult[*Module]), TypeDef: holderDef}}))
		load := func(name string) dagql.AnyResult {
			res, err := cache.LoadResultByResultID(ctx, env.session, srv, ids[name])
			require.NoError(t, err, name)
			require.Equal(t, encodings[name], persistedEncoding(t, ctx, cache, res).Envelope, "round %d: %s second save is identical", round, name)
			return res
		}

		group := load("check-group").Unwrap().(*CheckGroup)
		require.Equal(t, wsID, persistedRowID(t, cache, group.BoundWorkspace), "the exact bound workspace row")
		require.Equal(t, dirID, persistedRowID(t, cache, group.BoundWorkspace.Self().rootfs), "the workspace is rooted at the shared Directory row")
		require.Equal(t, "file:///ws", group.BoundWorkspace.Self().Address)
		require.Len(t, group.Checks, 2)
		require.Equal(t, "cold", group.Node.Name)
		require.Equal(t, modID, persistedRowID(t, cache, group.Node.Module))
		require.Equal(t, modID, persistedRowID(t, cache, group.Node.OriginalModule), "the defining module survives")
		require.NotNil(t, group.Node.DagqlServer, "the node's server is rebuilt from the saved module without running anything")
		restoredFailed := group.Checks[0]
		require.Equal(t, "lint", restoredFailed.Node.Name)
		require.Equal(t, "lints", restoredFailed.Node.Description)
		require.Same(t, group.Node, restoredFailed.Node.Parent, "members share the group's node table")
		require.Same(t, group.Node.DagqlServer, restoredFailed.Node.DagqlServer, "one server per saved module")
		require.True(t, restoredFailed.Completed)
		require.False(t, restoredFailed.Passed)
		require.True(t, restoredFailed.Error.Valid)
		require.Equal(t, errID, persistedRowID(t, cache, restoredFailed.Error.Value))
		require.Equal(t, "boom", restoredFailed.Error.Value.Self().Message, "the reported failure value survives")
		restoredPending := group.Checks[1]
		require.Equal(t, "format", restoredPending.Node.Name)
		require.True(t, restoredPending.IsGenerate)
		require.True(t, restoredPending.Node.IsGenerator)
		require.False(t, restoredPending.Completed)
		require.False(t, restoredPending.Error.Valid)

		ups := load("up-group").Unwrap().(*UpGroup)
		require.Equal(t, wsID, persistedRowID(t, cache, ups.BoundWorkspace))
		require.Len(t, ups.Ups, 1)
		require.Equal(t, "web", ups.Ups[0].Node.Name)
		require.True(t, ups.Ups[0].Node.IsUp)
		require.Equal(t, []PortForward{{Frontend: &frontend, Backend: 80, Protocol: NetworkProtocolTCP}}, ups.Ups[0].PortMappings)
		require.Same(t, ups.Node, ups.Ups[0].Node.Parent)

		terminals := load("terminal-group").Unwrap().(*TerminalGroup)
		require.Equal(t, wsID, persistedRowID(t, cache, terminals.BoundWorkspace))
		require.Len(t, terminals.Terminals, 1)
		require.Equal(t, "shell", terminals.Terminals[0].Node.Name)
		require.Same(t, terminals.Node, terminals.Terminals[0].Node.Parent)

		single := load("single-check").Unwrap().(*Check)
		require.Equal(t, errID, persistedRowID(t, cache, single.Error.Value))
		require.Equal(t, "cold", single.Node.Parent.Name, "a lone leaf keeps its parent node")
		require.Equal(t, "web", load("single-up").Unwrap().(*Up).Node.Name)
		require.Equal(t, "shell", load("single-terminal").Unwrap().(*TerminalTarget).Node.Name)

		restoredHolder := load("holder").Unwrap().(*ModuleObject)
		dirField, ok := restoredHolder.Fields["dir"].(dagql.AnyResult)
		require.True(t, ok, "the private field is an attached result")
		require.Equal(t, dirID, persistedRowID(t, cache, dirField), "the module object shares the same Directory row")
		require.Equal(t, "x", restoredHolder.Fields["label"])
		require.Equal(t, 1, snapshotOwners("shared-root"), "round %d: retained owners preserve one snapshot owner", round)
	}

	// Deliberate controls on the saved bytes.
	groupFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "check-group", Type: dagql.NewResultCallType((&CheckGroup{}).Type())}
	decodeGroup := func(t *testing.T, payload json.RawMessage) *CheckGroup {
		t.Helper()
		decoded, err := (&CheckGroup{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, ids["check-group"], groupFrame), payload)
		require.NoError(t, err)
		return decoded.(*CheckGroup)
	}
	var groupPayload map[string]any
	require.NoError(t, json.Unmarshal(encodings["check-group"].ObjectJSON, &groupPayload))
	t.Run("omitting the bound workspace leaves the group without one", func(t *testing.T) {
		lossy := map[string]any{}
		for k, v := range groupPayload {
			if k != "boundWorkspaceResultID" {
				lossy[k] = v
			}
		}
		raw, err := json.Marshal(lossy)
		require.NoError(t, err)
		group := decodeGroup(t, raw)
		require.Nil(t, group.BoundWorkspace.Self(), "runs would no longer resolve against the saved workspace")
		require.NotNil(t, decodeGroup(t, encodings["check-group"].ObjectJSON).BoundWorkspace.Self())
	})
	t.Run("omitting the defining module leaves no server to select from", func(t *testing.T) {
		var lossy map[string]any
		require.NoError(t, json.Unmarshal(encodings["check-group"].ObjectJSON, &lossy))
		nodes := lossy["tree"].(map[string]any)["nodes"].([]any)
		for _, node := range nodes {
			delete(node.(map[string]any), "moduleResultID")
			delete(node.(map[string]any), "originalModuleResultID")
		}
		raw, err := json.Marshal(lossy)
		require.NoError(t, err)
		group := decodeGroup(t, raw)
		require.Nil(t, group.Node.Module.Self())
		require.Nil(t, group.Node.DagqlServer)
		var dest dagql.AnyResult
		require.ErrorContains(t, group.Node.DagqlValue(ctx, &dest), "missing module", "action selection fails without its defining module")
	})

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
