package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentToolCallContext(t *testing.T) {
	ctx := t.Context()
	require.False(t, IsAgentToolCall(ctx))
	m := &MCP{}
	called := false
	_, failed := m.Call(ctx, []LLMTool{{
		Name: "push",
		Call: func(ctx context.Context, _ any) (any, error) {
			called = true
			require.True(t, IsAgentToolCall(ctx))
			return "ok", nil
		},
	}}, &LLMToolCall{Name: "push"})
	require.True(t, called)
	require.False(t, failed)
	require.False(t, IsAgentToolCall(ctx), "tool scope must not leak back into caller")
}
