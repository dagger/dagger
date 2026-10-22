package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingCallServer struct{ mockServer }

func (*failingCallServer) CurrentFunctionCall(context.Context) (*FunctionCall, error) {
	return nil, errors.New("client scope lookup failed")
}

func TestModuleProcessModule(t *testing.T) {
	t.Parallel()

	moduleFor := func(t *testing.T, fnCall *FunctionCall) bool {
		t.Helper()
		query := &Query{Server: &mockServer{moduleSource: &ModuleSource{ModuleName: "app"}, functionCall: fnCall}}
		mod, err := ModuleProcessModule(ContextWithQuery(t.Context(), query))
		require.NoError(t, err)
		return mod.Self() != nil
	}

	t.Run("a call that runs a module process passes its module", func(t *testing.T) {
		t.Parallel()
		fnCall := newFunctionCall(FunctionCall{Name: "build"})
		fnCall.SetModuleProcess()
		require.True(t, moduleFor(t, fnCall))
	})

	t.Run("a call of a container runtime does not", func(t *testing.T) {
		t.Parallel()
		require.False(t, moduleFor(t, newFunctionCall(FunctionCall{Name: "build"})))
	})

	t.Run("a client outside a function call does not, such as a module process", func(t *testing.T) {
		t.Parallel()
		require.False(t, moduleFor(t, nil))
	})

	t.Run("a failed call lookup fails", func(t *testing.T) {
		t.Parallel()
		_, err := ModuleProcessModule(ContextWithQuery(t.Context(), &Query{Server: &failingCallServer{}}))
		require.ErrorContains(t, err, "client scope lookup failed")
	})
}
