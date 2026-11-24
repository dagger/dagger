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

func TestIsModuleProcessExec(t *testing.T) {
	t.Parallel()
	marked := newFunctionCall(FunctionCall{Name: "build"})
	marked.SetModuleProcess()
	marked.SetProcessCallDigest("xxh3:abc")
	unmarked := newFunctionCall(FunctionCall{Name: "build"})
	unmarked.SetModuleProcess()
	runtime := newFunctionCall(FunctionCall{Name: "build"})
	runtime.SetProcessCallDigest("xxh3:abc")

	for _, tc := range []struct {
		name   string
		fnCall *FunctionCall
		env    []string
		want   bool
	}{
		{"the exec the digest marks", marked, []string{"A=1", ModuleCallDigestEnv + "=xxh3:abc"}, true},
		{"the last setting wins", marked, []string{ModuleCallDigestEnv + "=xxh3:abc", ModuleCallDigestEnv + "="}, false},
		{"an exec without the digest, such as a build", marked, []string{"A=1"}, false},
		{"an exec with another call's digest", marked, []string{ModuleCallDigestEnv + "=xxh3:def"}, false},
		{"an empty digest", marked, []string{ModuleCallDigestEnv + "="}, false},
		{"any exec of a call given no digest", unmarked, []string{"A=1"}, true},
		{"no exec of a call that starts no module process", runtime, []string{ModuleCallDigestEnv + "=xxh3:abc"}, false},
		{"no exec outside a call", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, IsModuleProcessExec(tc.fnCall, tc.env))
		})
	}
	require.True(t, marked.MarksModuleProcess())
	require.False(t, unmarked.MarksModuleProcess())
	require.False(t, runtime.MarksModuleProcess())
}
