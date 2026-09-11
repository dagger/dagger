package core

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// relocationVisitor rewrites every declared row reference through a mapping
// and records what it was shown. A missing mapping is an error, which is how
// the later transfer batch will refuse a closure it cannot translate.
type relocationVisitor struct {
	mapping map[uint64]uint64
	seen    []relocatedRef
}

type relocatedRef struct {
	kind dagql.PersistedRefKind
	path string
	id   uint64
	role string
	key  string
}

func (r *relocationVisitor) visit(ref *dagql.PersistedRef) error {
	r.seen = append(r.seen, relocatedRef{kind: ref.Kind, path: ref.Path.String(), id: ref.ResultID, role: ref.Role, key: ref.RefKey})
	if ref.ResultID == 0 {
		return nil
	}
	mapped, ok := r.mapping[ref.ResultID]
	if !ok {
		return fmt.Errorf("no mapping for row %d at %s", ref.ResultID, ref.Path)
	}
	ref.ResultID = mapped
	return nil
}

func (r *relocationVisitor) childIDs() map[string]uint64 {
	ids := map[string]uint64{}
	for _, ref := range r.seen {
		if ref.kind == dagql.PersistedRefChild {
			ids[ref.path] = ref.id
		}
	}
	return ids
}

func (r *relocationVisitor) kinds() map[string]dagql.PersistedRefKind {
	kinds := map[string]dagql.PersistedRefKind{}
	for _, ref := range r.seen {
		kinds[ref.path] = ref.kind
	}
	return kinds
}

// coreRelocationRecord builds the stored record of an attached row exactly as
// the visitor contract sees it: identity, generic envelope, recorded call and
// declared storage links.
func coreRelocationRecord(t *testing.T, ctx context.Context, cache *dagql.Cache, res dagql.AnyResult) dagql.PersistedRecord {
	t.Helper()
	encoding := persistedEncoding(t, ctx, cache, res)
	frame, err := res.ResultCall()
	require.NoError(t, err)
	return dagql.PersistedRecord{
		ResultID:      persistedRowID(t, cache, res),
		Envelope:      encoding.Envelope,
		Call:          frame,
		SnapshotLinks: encoding.SnapshotLinks,
	}
}

func recordJSON(t *testing.T, rec dagql.PersistedRecord) string {
	t.Helper()
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	return string(data)
}

// TestPersistedCoreLazyPayloadRelocation rewrites the declared references of
// an actual registered core lazy payload and reads the rewritten bytes back.
// Enumerating the references a visitor reports is not the same as proving the
// payload it returns names the new rows: the rewritten bytes are what a
// receiving engine decodes and saves again. The mapping deliberately swaps two
// rows of the same type, so a position the visitor fails to rewrite decodes to
// an existing row with the wrong content rather than to an error.
func TestPersistedCoreLazyPayloadRelocation(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "reloc-lazy")
	ctx, cache, srv := env.open(t)

	dirA := env.directory(t, ctx, cache, srv, "reloc-dir-a", "snap-a")
	dirB := env.directory(t, ctx, cache, srv, "reloc-dir-b", "snap-b")
	fileA := attachStoredSnapshotTestValue(t, ctx, cache, srv, env.session, "reloc-file-a", storedSnapshotTestValue("File", "file-a", "/a.txt", false), true).(dagql.ObjectResult[*File])
	fileB := attachStoredSnapshotTestValue(t, ctx, cache, srv, env.session, "reloc-file-b", storedSnapshotTestValue("File", "file-b", "/b.txt", false), true).(dagql.ObjectResult[*File])
	ids := map[string]uint64{
		"dirA": persistedRowID(t, cache, dirA), "dirB": persistedRowID(t, cache, dirB),
		"fileA": persistedRowID(t, cache, fileA), "fileB": persistedRowID(t, cache, fileB),
	}

	lazy := &Directory{
		Platform: Platform{OS: "linux", Architecture: "arm64"},
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	lazy.Dir.setValue("/")
	lazy.Lazy = &DirectoryWithFileLazy{
		LazyState: NewLazyState(),
		Parent:    dirA,
		Source:    fileA,
		DestPath:  "/a.txt",
		Owner:     "root",
	}
	res := env.attach(t, ctx, cache, srv, "reloc-lazy-dir", lazy)
	rec := coreRelocationRecord(t, ctx, cache, res)
	before := recordJSON(t, rec)

	// Colliding namespaces: each A row's target is the other row of its own
	// type, so an unrewritten position still resolves.
	reloc := &relocationVisitor{mapping: map[uint64]uint64{
		rec.ResultID: rec.ResultID,
		ids["dirA"]:  ids["dirB"], ids["dirB"]: ids["dirA"],
		ids["fileA"]: ids["fileB"], ids["fileB"]: ids["fileA"],
	}}
	out, err := dagql.VisitEncodedReferences(rec, reloc.visit)
	require.NoError(t, err)
	require.Equal(t, map[string]uint64{
		"objectJSON.lazyJSON.parentResultID": ids["dirA"],
		"objectJSON.lazyJSON.sourceResultID": ids["fileA"],
	}, reloc.childIDs(), "the registered Directory visitor reaches the lazy payload's own positions")
	require.Equal(t, before, recordJSON(t, rec), "the input record is untouched")

	// The rewritten bytes name the B rows. This is the untouched second-save
	// form: a receiving engine can copy these bytes without decoding them.
	var rewritten struct {
		LazyKind string                         `json:"lazyKind"`
		LazyJSON persistedDirectoryWithFileLazy `json:"lazyJSON"`
	}
	require.NoError(t, json.Unmarshal(out.Envelope.ObjectJSON, &rewritten))
	require.Equal(t, ids["dirB"], rewritten.LazyJSON.ParentResultID)
	require.Equal(t, ids["fileB"], rewritten.LazyJSON.SourceResultID)
	require.Equal(t, "/a.txt", rewritten.LazyJSON.DestPath, "plain data survives the rewrite")
	require.NotContains(t, string(out.Envelope.ObjectJSON), fmt.Sprintf(`"parentResultID":%d`, ids["dirA"]))

	// Typed decode of the rewritten bytes reaches the intended rows, with
	// their actual content, not merely their identities.
	decodedTyped, err := (&Directory{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, out.ResultID, out.Call), out.Envelope.ObjectJSON)
	require.NoError(t, err)
	decoded, ok := decodedTyped.(*Directory)
	require.True(t, ok)
	decodedLazy, ok := decoded.Lazy.(*DirectoryWithFileLazy)
	require.True(t, ok, "the lazy kind survives relocation")
	require.Equal(t, ids["dirB"], persistedRowID(t, cache, decodedLazy.Parent))
	parentSnapshot, ok := decodedLazy.Parent.Self().snapshotIdentity()
	require.True(t, ok)
	require.Equal(t, "snap-b", parentSnapshot, "the parent is the B row's actual content, not just its identity")
	require.Equal(t, ids["fileB"], persistedRowID(t, cache, decodedLazy.Source))
	sourceSnapshot, ok := decodedLazy.Source.Self().snapshotIdentity()
	require.True(t, ok)
	require.Equal(t, "file-b", sourceSnapshot)
	require.Equal(t, "/a.txt", decodedLazy.DestPath, "plain data is unchanged")
	require.Equal(t, "root", decodedLazy.Owner)

	// Typed second save: re-encoding the decoded value writes the B rows.
	reencoded, err := decoded.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, out.ResultID, out.Call))
	require.NoError(t, err)
	require.Equal(t, string(out.Envelope.ObjectJSON), string(reencoded.JSON), "the typed second save matches the relocated bytes")

	t.Run("an omitted mapping fails without partial writes", func(t *testing.T) {
		partial := &relocationVisitor{mapping: map[uint64]uint64{rec.ResultID: rec.ResultID, ids["dirA"]: ids["dirB"]}}
		_, err := dagql.VisitEncodedReferences(rec, partial.visit)
		require.ErrorContains(t, err, fmt.Sprintf("no mapping for row %d", ids["fileA"]))
		require.Equal(t, before, recordJSON(t, rec), "a failed relocation leaves the record alone")
	})
}

// TestPersistedModuleObjectPayloadRelocation rewrites the declared references
// of an actual ModuleObject payload: an attached child result, a private
// handle-form typed ID and a private recipe-form ID, with exact numbers and
// opaque strings alongside them. The mapping swaps two rows of the same type,
// so a position the visitor misses decodes to the wrong existing row. Recipe
// IDs are typed call descriptions with no row of their own and must pass
// through untouched.
func TestPersistedModuleObjectPayloadRelocation(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "reloc-modobj")
	ctx, cache, srv := env.open(t)

	dirA := env.directory(t, ctx, cache, srv, "reloc-obj-dir-a", "obj-snap-a")
	dirB := env.directory(t, ctx, cache, srv, "reloc-obj-dir-b", "obj-snap-b")
	handleA := env.directory(t, ctx, cache, srv, "reloc-obj-handle-a", "obj-handle-a")
	handleB := env.directory(t, ctx, cache, srv, "reloc-obj-handle-b", "obj-handle-b")
	ids := map[string]uint64{
		"dirA": persistedRowID(t, cache, dirA), "dirB": persistedRowID(t, cache, dirB),
		"handleA": persistedRowID(t, cache, handleA), "handleB": persistedRowID(t, cache, handleB),
	}

	handleAID, err := handleA.ID()
	require.NoError(t, err)
	require.NotZero(t, handleAID.EngineResultID())
	recipeID, err := dirA.ID()
	require.NoError(t, err)
	recipeOnly := call.New().Append(recipeID.Type().ToAST(), "recipeField")
	recipeEncoded, err := recipeOnly.Encode()
	require.NoError(t, err)

	objDef := NewObjectTypeDef("Holder", "", nil)
	mod := &Module{NameField: "reloc", Deps: NewSchemaBuilder(nil, nil)}
	modRes, err := dagql.NewObjectResultForCall(mod, srv, moduleObjectTestSyntheticCall("relocModule", mod))
	require.NoError(t, err)

	const bigInt = "9007199254740993"
	obj := &ModuleObject{
		Module:  modRes,
		TypeDef: objDef,
		Fields: map[string]any{
			// A declared-looking child the module returned: an attached row.
			"child": dirA,
			// Private state: a handle naming a row, a recipe naming no row,
			// and exact numeric and opaque leaves beside them.
			"private": map[string]any{
				"handle": handleAID,
				"recipe": recipeOnly,
				"counts": []any{json.Number(bigInt), json.Number("-1")},
				"label":  "9007199254740993",
			},
		},
	}
	payload, err := obj.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
	require.NoError(t, err)

	family, ok := dagql.PersistedObjectFamilyFor(&ModuleObject{})
	require.True(t, ok, "ModuleObject has a registered payload family")
	envelopeVersion := persistedEncoding(t, ctx, cache, dirA).Envelope.Version
	rec := dagql.PersistedRecord{
		ResultID: ids["dirA"] + 1000,
		Envelope: dagql.PersistedResultEnvelope{
			Version:     envelopeVersion,
			Kind:        "object_self",
			TypeName:    "Holder",
			ObjectCodec: family.Name,
			ObjectJSON:  payload.JSON,
		},
	}
	before := recordJSON(t, rec)

	reloc := &relocationVisitor{mapping: map[uint64]uint64{
		rec.ResultID: rec.ResultID,
		ids["dirA"]:  ids["dirB"], ids["dirB"]: ids["dirA"],
		ids["handleA"]: ids["handleB"], ids["handleB"]: ids["handleA"],
	}}
	out, err := dagql.VisitEncodedReferences(rec, reloc.visit)
	require.NoError(t, err)
	require.Equal(t, map[string]uint64{
		"objectJSON.fields.child.resultID":               ids["dirA"],
		"objectJSON.fields.private.fields.handle.callID": ids["handleA"],
	}, reloc.childIDs(), "the module object visitor reports the attached child and the handle, and nothing else")
	require.Equal(t, before, recordJSON(t, rec), "the input record is untouched")

	var rewritten persistedModuleObjectPayload
	require.NoError(t, dagql.UnmarshalLosslessJSON(out.Envelope.ObjectJSON, &rewritten))
	require.Equal(t, ids["dirB"], rewritten.Fields["child"].ResultID)
	private := rewritten.Fields["private"].Fields
	require.Equal(t, recipeEncoded, private["recipe"].CallID, "a recipe ID names no row and is unchanged")
	require.Equal(t, bigInt, string(private["counts"].Items[0].ScalarJSON), "exact numbers are data, not references")
	require.Equal(t, `"`+bigInt+`"`, string(private["label"].ScalarJSON), "a string that looks like a number is not a reference")

	relocatedHandle := &call.ID{}
	require.NoError(t, relocatedHandle.Decode(private["handle"].CallID))
	require.True(t, relocatedHandle.IsHandle())
	require.Equal(t, ids["handleB"], relocatedHandle.EngineResultID(), "the handle names the B row")
	require.Equal(t, handleAID.Type().ToAST().String(), relocatedHandle.Type().ToAST().String(), "with its exact recursive type")

	decodedTyped, err := obj.DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, out.ResultID, nil), out.Envelope.ObjectJSON)
	require.NoError(t, err)
	decoded, ok := decodedTyped.(*ModuleObject)
	require.True(t, ok)
	child, ok := decoded.Fields["child"].(dagql.AnyResult)
	require.True(t, ok)
	require.Equal(t, ids["dirB"], persistedRowID(t, cache, child))
	childDir, ok := child.Unwrap().(*Directory)
	require.True(t, ok)
	childSnapshot, ok := childDir.snapshotIdentity()
	require.True(t, ok)
	require.Equal(t, "obj-snap-b", childSnapshot, "the decoded child is the B row's actual content")
	decodedPrivate, ok := decoded.Fields["private"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, json.Number(bigInt), decodedPrivate["counts"].([]any)[0], "numbers stay exact through the relocated decode")

	reencoded, err := decoded.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, out.ResultID, nil))
	require.NoError(t, err)
	require.Equal(t, string(out.Envelope.ObjectJSON), string(reencoded.JSON), "the typed second save matches the relocated bytes")

	t.Run("an omitted mapping fails without partial writes", func(t *testing.T) {
		partial := &relocationVisitor{mapping: map[uint64]uint64{rec.ResultID: rec.ResultID, ids["dirA"]: ids["dirB"]}}
		_, err := dagql.VisitEncodedReferences(rec, partial.visit)
		require.ErrorContains(t, err, fmt.Sprintf("no mapping for row %d", ids["handleA"]))
		require.Equal(t, before, recordJSON(t, rec))
	})
}

// TestPersistedActionPayloadRelocationKeepsNodeIndices relocates an action
// group's declared row references and checks that the shared node table's
// local indices are left alone. A node index is a position inside one
// payload's own table, not a cache row, so rewriting it would corrupt the
// parent sharing the table encodes.
func TestPersistedActionPayloadRelocationKeepsNodeIndices(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "reloc-actions")
	ctx, cache, srv := env.open(t)

	modA := env.attach(t, ctx, cache, srv, "reloc-mod-a", &Module{NameField: "a", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	modB := env.attach(t, ctx, cache, srv, "reloc-mod-b", &Module{NameField: "b", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	dirRes := env.directory(t, ctx, cache, srv, "reloc-ws-root", "ws-root")
	wsA := env.attach(t, ctx, cache, srv, "reloc-ws-a", &Workspace{rootfs: dirRes, Address: "file:///a", Cwd: "/"}).(dagql.ObjectResult[*Workspace])
	wsB := env.attach(t, ctx, cache, srv, "reloc-ws-b", &Workspace{rootfs: dirRes, Address: "file:///b", Cwd: "/"}).(dagql.ObjectResult[*Workspace])
	ids := map[string]uint64{
		"modA": persistedRowID(t, cache, modA), "modB": persistedRowID(t, cache, modB),
		"wsA": persistedRowID(t, cache, wsA), "wsB": persistedRowID(t, cache, wsB),
	}

	root := &ModTreeNode{Name: "root", Module: modA, OriginalModule: modA}
	lint := &ModTreeNode{Name: "lint", Parent: root, Module: modA, IsCheck: true}
	format := &ModTreeNode{Name: "format", Parent: root, Module: modA, IsCheck: true}
	group := env.attach(t, ctx, cache, srv, "reloc-check-group", &CheckGroup{
		Node:           root,
		Checks:         []*Check{{Node: lint}, {Node: format}},
		BoundWorkspace: wsA,
	})
	rec := coreRelocationRecord(t, ctx, cache, group)
	before := recordJSON(t, rec)

	var original struct {
		Tree struct {
			Nodes []struct {
				Name                   string `json:"name"`
				ParentID               int    `json:"parentID"`
				ModuleResultID         uint64 `json:"moduleResultID"`
				OriginalModuleResultID uint64 `json:"originalModuleResultID"`
			} `json:"nodes"`
		} `json:"tree"`
		Checks []struct {
			NodeID int `json:"nodeID"`
		} `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &original))
	require.NotEmpty(t, original.Tree.Nodes)

	reloc := &relocationVisitor{mapping: map[uint64]uint64{
		rec.ResultID: rec.ResultID,
		ids["modA"]:  ids["modB"], ids["modB"]: ids["modA"],
		ids["wsA"]: ids["wsB"], ids["wsB"]: ids["wsA"],
	}}
	out, err := dagql.VisitEncodedReferences(rec, reloc.visit)
	require.NoError(t, err)
	require.Equal(t, before, recordJSON(t, rec), "the input record is untouched")

	// Only rows are reported. No node table position appears here, which is
	// what keeps the indices out of any mapping a receiving engine applies.
	require.Equal(t, map[string]uint64{
		"objectJSON.tree.nodes[0].moduleResultID":         ids["modA"],
		"objectJSON.tree.nodes[0].originalModuleResultID": ids["modA"],
		"objectJSON.tree.nodes[1].moduleResultID":         ids["modA"],
		"objectJSON.tree.nodes[2].moduleResultID":         ids["modA"],
		"objectJSON.boundWorkspaceResultID":               ids["wsA"],
	}, reloc.childIDs())

	var relocated struct {
		Tree struct {
			Nodes []struct {
				Name                   string `json:"name"`
				ParentID               int    `json:"parentID"`
				ModuleResultID         uint64 `json:"moduleResultID"`
				OriginalModuleResultID uint64 `json:"originalModuleResultID"`
			} `json:"nodes"`
		} `json:"tree"`
		Checks []struct {
			NodeID int `json:"nodeID"`
		} `json:"checks"`
		BoundWorkspaceResultID uint64 `json:"boundWorkspaceResultID"`
	}
	require.NoError(t, json.Unmarshal(out.Envelope.ObjectJSON, &relocated))
	require.Equal(t, ids["wsB"], relocated.BoundWorkspaceResultID)
	require.Len(t, relocated.Tree.Nodes, len(original.Tree.Nodes))
	for i, node := range relocated.Tree.Nodes {
		require.Equal(t, original.Tree.Nodes[i].Name, node.Name)
		require.Equal(t, original.Tree.Nodes[i].ParentID, node.ParentID, "node %d keeps its table position", i)
		if original.Tree.Nodes[i].ModuleResultID != 0 {
			require.Equal(t, ids["modA"], original.Tree.Nodes[i].ModuleResultID)
			require.Equal(t, ids["modB"], node.ModuleResultID, "node %d's defining module is a row and moves", i)
		}
		if original.Tree.Nodes[i].OriginalModuleResultID != 0 {
			require.Equal(t, ids["modB"], node.OriginalModuleResultID)
		}
	}
	for i, check := range relocated.Checks {
		require.Equal(t, original.Checks[i].NodeID, check.NodeID, "check %d still points at its own node", i)
		require.NotZero(t, check.NodeID, "the node table position is a real index")
	}

	decodedTyped, err := (&CheckGroup{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, out.ResultID, out.Call), out.Envelope.ObjectJSON)
	require.NoError(t, err)
	decoded, ok := decodedTyped.(*CheckGroup)
	require.True(t, ok)
	require.Equal(t, ids["wsB"], persistedRowID(t, cache, decoded.BoundWorkspace))
	require.Equal(t, "file:///b", decoded.BoundWorkspace.Self().Address, "the bound workspace is the B row's actual content")
	require.Equal(t, ids["modB"], persistedRowID(t, cache, decoded.Node.Module))
	require.Equal(t, "b", decoded.Node.Module.Self().NameField)
	require.Len(t, decoded.Checks, 2)
	require.Same(t, decoded.Node, decoded.Checks[0].Node.Parent, "the shared node table survives relocation")
	require.Same(t, decoded.Node, decoded.Checks[1].Node.Parent)
	require.Equal(t, "lint", decoded.Checks[0].Node.Name)
	require.Equal(t, "format", decoded.Checks[1].Node.Name)
}

// TestPersistedCoreStorageRolesAreClassified checks that the registered core
// visitors classify each family's own storage roles: an immutable output a
// receiving engine could be given a description of, versus local mutable
// backing that only means something on the engine that holds it.
func TestPersistedCoreStorageRolesAreClassified(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "reloc-roles")
	env.manager.mutableBySnapshotID = map[string]bkcache.MutableRef{
		"mirror-bare": &cacheVolumeTestMutableRef{cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{id: "mirror-bare", snapshotID: "mirror-bare"}},
	}
	ctx, cache, srv := env.open(t)

	dirRes := env.directory(t, ctx, cache, srv, "role-dir", "role-snap")
	mirror := NewRemoteGitMirror("https://example.com/org/repo.git")
	mirror.snapshot = env.manager.mutableBySnapshotID["mirror-bare"]
	mirrorRes := env.attach(t, ctx, cache, srv, "role-mirror", mirror)

	roles := func(res dagql.AnyResult) []relocatedRef {
		rec := coreRelocationRecord(t, ctx, cache, res)
		visitor := &relocationVisitor{mapping: map[uint64]uint64{rec.ResultID: rec.ResultID}}
		_, err := dagql.VisitEncodedReferences(rec, visitor.visit)
		require.NoError(t, err)
		var reported []relocatedRef
		for _, ref := range visitor.seen {
			if ref.kind == dagql.PersistedRefOutputRole || ref.kind == dagql.PersistedRefLocalBacking {
				reported = append(reported, ref)
			}
		}
		return reported
	}

	dirRoles := roles(dirRes)
	require.Len(t, dirRoles, 1)
	require.Equal(t, dagql.PersistedRefOutputRole, dirRoles[0].kind, "a Directory's snapshot is an immutable output")
	require.Equal(t, "snapshot", dirRoles[0].role)
	require.Equal(t, "role-snap", dirRoles[0].key)

	mirrorRoles := roles(mirrorRes)
	require.Len(t, mirrorRoles, 1)
	require.Equal(t, dagql.PersistedRefLocalBacking, mirrorRoles[0].kind, "a Git mirror's bare checkout is local mutable backing")
	require.Equal(t, "mirror-bare", mirrorRoles[0].key, "its key is this engine's own, never a transferable description")
}
