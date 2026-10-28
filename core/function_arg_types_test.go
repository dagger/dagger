package core

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type argTypeServerBase interface{ Server }

type argTypeMemoServer struct {
	argTypeServerBase
	memo *SchemaBuilderMemo
}

func (s *argTypeMemoServer) SessionSchemaBuilderMemo(context.Context) (*SchemaBuilderMemo, error) {
	return s.memo, nil
}

func TestFunctionTypeMetadataSharedWithinSession(t *testing.T) {
	f := newModuleOwnershipTest(t)
	server := &argTypeMemoServer{argTypeServerBase: f.root.Server, memo: NewSchemaBuilderMemo()}
	f.root.Server = server
	ctx := f.session("arg-type-memo")
	module := f.module(ctx, "fixture", moduleOwnershipModuleOpts{})
	def := module.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	function := def.Functions[0].Self()
	first, err := NewModFunction(ctx, module, def, function)
	require.NoError(t, err)

	// Installing this same module for concurrent nested clients reuses the
	// resolved immutable type; each function wrapper remains independent.
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			again, err := NewModFunction(ctx, module, def, function)
			if !assertNoTypeError(t, err) {
				return
			}
			require.NotSame(t, first, again)
			require.Same(t, first.returnType, again.returnType)
			require.Same(t, first.args["label"].modType, again.args["label"].modType)
		})
	}
	wg.Wait()

	// An API view change or another session must resolve its own metadata.
	f.dag.View = "v0.19.0"
	otherView, err := NewModFunction(ctx, module, def, function)
	require.NoError(t, err)
	require.NotSame(t, first.returnType, otherView.returnType)
	f.dag.View = ""
	server.memo = NewSchemaBuilderMemo()
	otherSession, err := NewModFunction(ctx, module, def, function)
	require.NoError(t, err)
	require.NotSame(t, first.returnType, otherSession.returnType)

	// The same name on a different operational module does not alias.
	otherModule := f.module(ctx, "fixture", moduleOwnershipModuleOpts{variant: "other"})
	typ, ok, err := NewUserMod(otherModule).ModTypeFor(ctx, function.ReturnType.Self(), true)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotSame(t, otherSession.returnType, typ)
}

func assertNoTypeError(t *testing.T, err error) bool {
	t.Helper()
	if err != nil {
		t.Error(err)
		return false
	}
	return true
}

func TestCanonicalArgumentTypeRetriesFailedResolution(t *testing.T) {
	memo := NewSchemaBuilderMemo()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := memo.canonicalArgType("type", func() (*TypeDef, error) { return nil, canceled.Err() })
	require.ErrorIs(t, err, context.Canceled)
	def := &TypeDef{Kind: TypeDefKindString}
	got, err := memo.canonicalArgType("type", func() (*TypeDef, error) { return def, nil })
	require.NoError(t, err)
	require.Same(t, def, got)
	got, err = memo.canonicalArgType("type", func() (*TypeDef, error) {
		t.Error("successful canonicalization should be reused")
		return nil, nil
	})
	require.NoError(t, err)
	require.Same(t, def, got)
}

func TestUserDefaultWithoutVariablesNeedsNoHostMetadata(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("no-default-host")
	module := f.module(ctx, "fixture", moduleOwnershipModuleOpts{})
	def := module.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	fn, err := NewModFunction(ctx, module, def, def.Functions[0].Self())
	require.NoError(t, err)
	// There is deliberately no client or query on this context. An empty
	// defaults namespace has no environment expansion requiring host access.
	value, ok, err := fn.UserDefault(context.Background(), "label")
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, value)
}
