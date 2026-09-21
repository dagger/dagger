package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// These controls close the gaps the early review named for the generic
// contract: handle relocation of a recursive wrapper type, a declared
// interface list against an installed interface, and an attached absent row
// that owns a dependency and inherits a session-resource requirement.

func TestVisitPersistedCallIDPreservesRecursiveWrapper(t *testing.T) {
	for _, typ := range []*ast.Type{
		{Elem: &ast.Type{Elem: &ast.Type{NamedType: "String", NonNull: true}, NonNull: true}, NonNull: true},
		{Elem: &ast.Type{Elem: &ast.Type{NamedType: "PersistCodecObj"}}, NonNull: false},
		{Elem: &ast.Type{NamedType: "Int", NonNull: true}},
	} {
		t.Run(typ.String(), func(t *testing.T) {
			handle, err := call.NewEngineResultID(17, call.NewType(typ)).Encode()
			require.NoError(t, err)
			translated := handle
			changed, err := VisitPersistedCallID(func(ref *PersistedRef) error {
				require.Equal(t, uint64(17), ref.ResultID)
				ref.ResultID = 901
				return nil
			}, PersistedRefChild, PersistedRefPath{}.Field("field"), &translated)
			require.NoError(t, err)
			require.True(t, changed)
			var decoded call.ID
			require.NoError(t, decoded.Decode(translated))
			require.True(t, decoded.IsHandle())
			require.Equal(t, uint64(901), decoded.EngineResultID())
			require.Equal(t, typ.String(), decoded.Type().ToAST().String(), "the exact recursive wrapper survives relocation")
			var original call.ID
			require.NoError(t, original.Decode(handle))
			require.Equal(t, uint64(17), original.EngineResultID(), "the input is untouched")
		})
	}
}

func TestPersistedInterfaceListUsesInstalledInterface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	installIface := func(srv *Server) *Interface {
		srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
		Fields[*persistCodecObj]{
			Func("name", func(_ context.Context, self *persistCodecObj, _ struct{}) (String, error) {
				return String(self.Name), nil
			}),
		}.Install(srv)
		iface := NewInterface("PersistNamed", "Something with a name.")
		iface.AddField(InterfaceFieldSpec{FieldSpec: FieldSpec{Name: "name", Type: String("")}})
		srv.InstallInterface(iface)
		return iface
	}
	iface := installIface(srv)
	objType, ok := srv.ObjectType("PersistCodecObj")
	require.True(t, ok)
	require.True(t, iface.Satisfies(objType, srv.View), "the concrete object implements the installed interface")
	require.True(t, iface.HasImplementor("PersistCodecObj"))

	impl := persistedListTestResult(t, ctx, cache, srv, "iface-impl", &persistCodecObj{Name: "impl"})
	implID, err := cache.PersistedResultID(impl)
	require.NoError(t, err)
	// The declared element is the installed interface; the item is the
	// concrete object's own row.
	listType := &ResultCallType{Elem: &ResultCallType{NamedType: iface.TypeName(), NonNull: true}, NonNull: true}
	frame := &ResultCall{Kind: ResultCallKindField, Field: "iface-list", Type: listType}
	list, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(DynamicResultArrayOutput{Elem: iface.Typed(), Values: []AnyResult{impl}}, frame)
	})
	require.NoError(t, err)
	listID, err := cache.PersistedResultID(list)
	require.NoError(t, err)
	freshType := list.Type().String()
	freshElem := list.Unwrap().(Enumerable).Element().Type().String()
	encoding := persistedListTestEncoding(t, ctx, cache, list)
	var env PersistedResultEnvelope
	require.NoError(t, json.Unmarshal(encoding, &env))
	require.Equal(t, "PersistNamed", env.TypeName)
	require.Equal(t, persistedResultKindRef, env.Items[0].Kind, "the concrete item is its own attached row")
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.Close(ctx))

	ctx, cache, srv = persistedListTestCache(t, path)
	installIface(srv)
	restored, err := cache.LoadResultByResultID(ctx, "test-session", srv, listID)
	require.NoError(t, err)
	require.Equal(t, "[PersistNamed!]!", freshType)
	require.Equal(t, freshType, restored.Type().String(), "the declared interface list type survives")
	require.Equal(t, freshElem, restored.Unwrap().(Enumerable).Element().Type().String(), "the declared element stays the interface, not the first item's type")
	item, err := restored.NthValue(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, implID, uint64(item.cacheSharedResult().id), "the item is the concrete object's exact row")
	require.Equal(t, "PersistCodecObj", item.Type().Name(), "the item keeps its concrete type and schema")
	require.Equal(t, "impl", item.Unwrap().(*persistCodecObj).Name)
	require.Equal(t, encoding, persistedListTestEncoding(t, ctx, cache, restored), "the second save is byte-identical")

	t.Run("without the installed interface the declared type cannot be read by the concrete class", func(t *testing.T) {
		bare := newDagqlServerForTest(t, &persistCodecRoot{})
		bare.InstallObject(NewClass(bare, ClassOpts[*persistCodecObj]{}))
		_, ok := bare.InterfaceType("PersistNamed")
		require.False(t, ok)
		// Decoding the list still rebuilds the declared type from the recorded
		// call: no schema is consulted for the descriptor, so the item's class
		// is what resolves; the declaration is not inferred from the item.
		decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, bare, listID, frame.clone(), env)
		require.NoError(t, err)
		require.Equal(t, freshType, decoded.Type().String())
	})
}

func TestPersistedAttachedNullKeepsOwnedDependencyAndResource(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	handle := cacheTestVolatileSessionResourceHandle("persist-null-owned")

	newServer := func() *Server { return newPersistResourceScopedTestServer(handle) }

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
	require.NoError(t, err)
	srvA := newServer()
	ctxA := ContextWithCall(ctx, &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&persistCodecRoot{}).Type()), Field: "persist-null-owned-root"})
	ctxA = srvToContext(ContextWithCache(ctxA, cacheA), srvA)
	require.NoError(t, cacheA.BindSessionResource(ctxA, "test-session", "dagql-test-client", handle, "bound"))

	var scoped ObjectResult[*persistResourceScopedObj]
	require.NoError(t, srvA.Select(ctxA, srvA.Root(), &scoped, Selector{Field: "resourceScopedChain", Args: []NamedInput{{Name: "level", Value: NewInt(0)}}}))
	scopedID := uint64(scoped.cacheSharedResult().id)
	// The absent row is created through the cache with the scoped row as its
	// recorded receiver, exactly as a field selection on that object would
	// record it: ownership of the receiver is derived from the frame.
	absentFrame := &ResultCall{
		Kind:     ResultCallKindField,
		Field:    "absentName",
		Type:     &ResultCallType{NamedType: "String"},
		Receiver: &ResultCallRef{ResultID: scopedID},
	}
	absent, err := cacheA.GetOrInitCall(ctxA, "test-session", srvA, &CallRequest{ResultCall: absentFrame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(Null[String](), absentFrame)
	})
	require.NoError(t, err)
	absentShared := absent.cacheSharedResult()
	require.NotNil(t, absentShared)
	absentID := uint64(absentShared.id)
	require.NotZero(t, absentID)
	_, present := absent.DerefValue()
	require.False(t, present)
	freshType := absent.Type().String()

	depIDs := func(shared *sharedResult) []uint64 {
		var ids []uint64
		for depID := range shared.deps {
			ids = append(ids, uint64(depID))
		}
		return ids
	}
	require.Contains(t, depIDs(absentShared), scopedID, "the absent row owns its receiver before the save")
	require.True(t, cacheTestSessionResourceSetContains(absentShared.requiredSessionResources, handle), "the absent row inherits the requirement before the save")

	encoding, err := DefaultPersistedSelfCodec.EncodeResult(ctxA, cacheA, absent)
	require.NoError(t, err)
	require.Equal(t, persistedResultKindNull, encoding.Envelope.Kind)
	require.Equal(t, absentID, encoding.Envelope.ResultID)

	assertCacheRequiredSessionResourcesExact(t, cacheA)
	cacheTestReleaseSession(t, cacheA, ctxA)
	require.NoError(t, cacheA.persistCurrentState(ctx))
	require.NoError(t, cacheA.Close(context.Background()))

	for round := 1; round <= 2; round++ {
		cacheB, err := NewCache(ctx, dbPath, nil, nil)
		require.NoError(t, err)
		require.Equal(t, CachePersistenceResetNone, cacheB.PersistenceResetReason())
		srvB := newServer()
		ctxB := srvToContext(ContextWithCache(ctx, cacheB), srvB)

		// An unbound session cannot use the row; its inherited requirement
		// survived the restart.
		_, err = cacheB.LoadResultByResultID(ctxB, "unbound-session", srvB, absentID)
		require.Error(t, err, "round %d: the requirement is enforced on the restored absent row", round)

		require.NoError(t, cacheB.BindSessionResource(ctxB, "test-session", "dagql-test-client", handle, "bound"))
		restored, err := cacheB.LoadResultByResultID(ctxB, "test-session", srvB, absentID)
		require.NoError(t, err)
		require.Equal(t, absentID, uint64(restored.cacheSharedResult().id))
		require.Equal(t, freshType, restored.Type().String(), "round %d: restored type matches the fresh nullable", round)
		_, present := restored.DerefValue()
		require.False(t, present)
		require.Contains(t, depIDs(restored.cacheSharedResult()), scopedID, "round %d: the restored absent row still owns its receiver", round)
		require.True(t, cacheTestSessionResourceSetContains(restored.cacheSharedResult().requiredSessionResources, handle), "round %d: the restored absent row still requires the handle", round)
		assertCacheRequiredSessionResourcesExact(t, cacheB)

		reencoded, err := DefaultPersistedSelfCodec.EncodeResult(ctxB, cacheB, restored)
		require.NoError(t, err)
		require.Equal(t, encoding.Envelope, reencoded.Envelope, "round %d: the second save re-emits the same null with the same identity", round)

		cacheTestReleaseSession(t, cacheB, ctxB)
		require.NoError(t, cacheB.persistCurrentState(ctx))
		require.NoError(t, cacheB.Close(context.Background()))
	}
}
