package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
)

type persistedSelfReference struct{ Base AnyResult }

func (*persistedSelfReference) Type() *ast.Type {
	return &ast.Type{NamedType: "SelfReference", NonNull: true}
}
func (obj *persistedSelfReference) InitializeResultReference(self AnyResult) {
	if obj.Base == nil {
		obj.Base = self
	}
}
func (obj *persistedSelfReference) AttachDependencyResults(_ context.Context, self AnyResult, _ func(AnyResult) (AnyResult, error)) ([]AnyResult, error) {
	obj.InitializeResultReference(self)
	return nil, nil
}
func (*persistedSelfReference) EncodePersistedObject(context.Context, PersistedObjectCache) (PersistedObjectEncoding, error) {
	return PersistedObjectEncoding{JSON: json.RawMessage(`{}`)}, nil
}
func (*persistedSelfReference) DecodePersistedObject(context.Context, *Server, uint64, *ResultCall, json.RawMessage) (Typed, error) {
	return &persistedSelfReference{}, nil
}

func TestCacheRestoresSelfReference(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	for attempt := range 2 {
		cache, err := NewCache(ctx, dbPath, nil, nil)
		assert.NilError(t, err)
		srv := newPersistCodecImportTestServer()
		srv.InstallObject(NewClass(srv, ClassOpts[*persistedSelfReference]{}))
		Fields[*persistCodecRoot]{Func("selfReference", func(context.Context, *persistCodecRoot, struct{}) (*persistedSelfReference, error) {
			assert.Equal(t, attempt, 0, "persisted result must not execute its resolver again")
			return &persistedSelfReference{}, nil
		}).IsPersistable()}.Install(srv)
		callCtx := srvToContext(ContextWithCache(ctx, cache), srv)
		var result ObjectResult[*persistedSelfReference]
		assert.NilError(t, srv.Select(callCtx, srv.Root(), &result, Selector{Field: "selfReference"}))
		obj := result.Self()
		assert.Assert(t, obj.Base != nil)
		assert.Assert(t, obj.Base.Unwrap() == obj)
		baseID, err := obj.Base.ID()
		assert.NilError(t, err)
		id, err := result.ID()
		assert.NilError(t, err)
		assert.Equal(t, baseID.EngineResultID(), id.EngineResultID())
		cacheTestReleaseSession(t, cache, callCtx)
		assert.NilError(t, cache.persistCurrentState(ctx))
		assert.NilError(t, cache.Close(context.Background()))
	}
}
